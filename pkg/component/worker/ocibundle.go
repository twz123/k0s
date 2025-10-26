// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/avast/retry-go"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	"github.com/containerd/platforms"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/component/prober"
	workercontainerd "github.com/k0sproject/k0s/pkg/component/worker/containerd"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
)

const (
	// Follows a list of labels we use to control imported images.
	ImagePinnedLabel      = "io.cri-containerd.pinned"
	ImageSourcePathsLabel = "io.k0sproject.ocibundle-paths"
)

// OCIBundleReconciler tries to import OCI bundle into the running containerd instance
type OCIBundleReconciler struct {
	ociBundleDir      string
	containerdAddress string
	log               *logrus.Entry
	alreadyImported   map[string]time.Time
	stop              func()
	*prober.EventEmitter
}

var _ manager.Component = (*OCIBundleReconciler)(nil)

// NewOCIBundleReconciler builds new reconciler
func NewOCIBundleReconciler(vars *config.CfgVars) *OCIBundleReconciler {
	return &OCIBundleReconciler{
		ociBundleDir:      vars.OCIBundleDir,
		containerdAddress: workercontainerd.Address(vars.RunDir),
		log:               logrus.WithField("component", "OCIBundleReconciler"),
		EventEmitter:      prober.NewEventEmitter(),
		alreadyImported:   map[string]time.Time{},
	}
}

func (a *OCIBundleReconciler) Init(_ context.Context) error {
	return dir.Init(a.ociBundleDir, constant.ManifestsDirMode)
}

// containerdClient returns a connected containerd client.
func (a *OCIBundleReconciler) containerdClient(ctx context.Context) (*containerd.Client, error) {
	var client *containerd.Client
	if err := retry.Do(func() (err error) {
		client, err = containerd.New(
			a.containerdAddress,
			containerd.WithDefaultNamespace("k8s.io"),
			containerd.WithDefaultPlatform(
				platforms.Only(platforms.DefaultSpec()),
			),
		)
		if err != nil {
			return fmt.Errorf("failed to connect to containerd: %w", err)
		}
		if _, err = client.ListImages(ctx); err != nil {
			_ = client.Close()
			return fmt.Errorf("failed to communicate with containerd: %w", err)
		}
		return nil
	}, retry.Context(ctx), retry.Delay(time.Second*5)); err != nil {
		return nil, err
	}
	return client, nil
}

// loadOne connects to containerd and imports the provided OCI bundle.
func (a *OCIBundleReconciler) loadOne(ctx context.Context, fpath string, lastImportedModTime time.Time) (modTime time.Time, _ error) {
	if finfo, err := os.Stat(fpath); err != nil {
		return modTime, err
	} else {
		modTime = finfo.ModTime()
	}

	if !lastImportedModTime.IsZero() && lastImportedModTime.Equal(modTime) {
		return lastImportedModTime, nil
	}

	client, err := a.containerdClient(ctx)
	if err != nil {
		return modTime, fmt.Errorf("failed to create containerd client: %w", err)
	}
	defer client.Close()
	if err := a.unpackBundle(ctx, client, fpath, modTime); err != nil {
		return modTime, fmt.Errorf("failed to process OCI bundle: %w", err)
	}

	return modTime, nil
}

// unpin unpins containerd images from the image store. we unpin an image if
// the file from where it was imported no longer exists or the file content
// has been changed.
func (a *OCIBundleReconciler) unpinAll(ctx context.Context) error {
	client, err := a.containerdClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create containerd client: %w", err)
	}
	defer client.Close()

	isvc := client.ImageService()
	images, err := isvc.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list images: %w", err)
	}

	for _, image := range images {
		if err := a.unpinOne(ctx, image, isvc); err != nil {
			a.log.WithError(err).Errorf("Failed to unpin image %s", image.Name)
		}
	}
	return nil
}

// unpinOne checks if we can unpin the provided image and if so unpins it.
func (a *OCIBundleReconciler) unpinOne(ctx context.Context, image images.Image, isvc images.Store) error {
	// if this image isn't pinned, return immediately.
	if v, pin := image.Labels[ImagePinnedLabel]; !pin || v != "pinned" {
		return nil
	}

	// extract the bundle paths from the image labels. if none has been found
	// then we don't own this image. return.
	sources, err := GetImageSources(image)
	if err != nil {
		return fmt.Errorf("failed to extract image source: %w", err)
	} else if len(sources) == 0 {
		return nil
	}

	// if any of the registered sources is still present, we can't unpin the image.
	// we just update the image label to remove references to the bundles that no
	// longer exist.
	if exists, err := sources.Exist(); err != nil {
		return fmt.Errorf("failed to check if sources exist: %w", err)
	} else if exists {
		if err := sources.Refresh(); err != nil {
			return fmt.Errorf("failed to refresh image sources: %w", err)
		}
		if err := SetImageSources(&image, sources); err != nil {
			return fmt.Errorf("failed to reset image sources: %w", err)
		}
		_, err := isvc.Update(ctx, image, "labels."+ImageSourcePathsLabel)
		return err
	}

	// all bundles referred by this image are no more, we can unpin it.
	a.log.Infof("Unpinning image %s", image.Name)
	a.EmitWithPayload("unpinning image", image.Name)
	delete(image.Labels, ImagePinnedLabel)
	delete(image.Labels, ImageSourcePathsLabel)
	_, err = isvc.Update(ctx, image)
	return err
}

// Starts initiate the OCI bundle loader. It does an initial load of the directory and
// once it is done, it starts a watcher on its own goroutine.
func (a *OCIBundleReconciler) Start(context.Context) error {
	imported := make(map[string]time.Time)

	stopErr := errors.New("OCI bundle importer is stopping")
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		wait.UntilWithContext(ctx, func(ctx context.Context) {
			if err := a.runImporter(ctx, imported); !errors.Is(err, stopErr) {
				a.log.WithError(err).Error("Failed to watch OCI bundle directory")
			}
		}, 30*time.Second)
	}()

	a.stop = func() {
		cancel(stopErr)
		<-done
	}

	return nil
}

func (a *OCIBundleReconciler) runImporter(ctx context.Context, imported map[string]time.Time) error {
	watcher, err := fsnotify.NewBufferedWatcher(10)
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	if err := watcher.Add(a.ociBundleDir); err != nil {
		return fmt.Errorf("failed to add watcher: %w", err)
	}

	var pendingBundles map[string]time.Time
	if paths, err := os.ReadDir(a.ociBundleDir); err != nil {
		return fmt.Errorf("failed to read bundles directory: %w", err)
	} else {
		now := time.Now()
		for _, path := range paths {
			path := filepath.Join(a.ociBundleDir, path.Name())
			pendingBundles[path] = now
		}
	}

	ctx, cancel := context.WithCancelCause(ctx)

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			err = errors.Join(err, watcher.Close())
		}()

		for {
			select {
			case err = <-watcher.Errors:
				cancel(err)
				return

			case ev := <-watcher.Events:
				due := time.Now().Add(10 * time.Second)
				mu.Lock()
				pendingBundles[ev.Name] = due
				mu.Unlock()

			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		for {
			var (
				nextPath string
				nextDue  time.Time
			)

			mu.Lock()
			for path, due := range pendingBundles {
				if due.Before(nextDue) || nextDue.IsZero() {
					nextPath, nextDue = path, due
				}
			}
			delete(pendingBundles, nextPath)
			mu.Unlock()

			select {
			case <-time.After(nextDue.Sub(time.Now())):
				mu.Lock()
				_, ok := pendingBundles[nextPath]
				mu.Unlock()
				if ok {
					continue
				}

				if err := a.loadOne(ctx, nextPath, nextDue); err != nil {
					a.log.WithError(err).Error("Failed to import OCI bundle")
					mu.Lock()
					if due, ok := pendingBundles[nextPath]; !ok || due.Equal(nextDue) {
						pendingBundles[nextPath] = time.Now().Add(1 * time.Minute)
					}
					mu.Unlock()
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

// unpackBundle imports the bundle into the containerd storage. imported images are
// pinned and labeled so we can control them later.
func (a *OCIBundleReconciler) unpackBundle(ctx context.Context, client *containerd.Client, bundlePath string, modtime time.Time) error {
	r, err := os.Open(bundlePath)
	if err != nil {
		return fmt.Errorf("can't open bundle file %s: %w", bundlePath, err)
	}
	defer r.Close()
	// WithSkipMissing allows us to skip missing blobs
	// Without this the importing would fail if the bundle does not images for compatible architectures
	// because the image manifest still refers to those. E.g. on arm64 containerd would still try to unpack arm/v8&arm/v7
	// images but would fail as those are not present on k0s airgap bundles.
	images, err := client.Import(ctx, r, containerd.WithSkipMissing())
	if err != nil {
		return fmt.Errorf("can't import bundle: %w", err)
	}

	fieldpaths := []string{
		"labels." + ImagePinnedLabel,
		"labels." + ImageSourcePathsLabel,
	}

	isvc := client.ImageService()
	for _, i := range images {
		// here we add a label to pin the image in the containerd storage and another
		// to indicate from which oci buncle (file path) the image was imported from.
		a.log.Infof("Imported image %s", i.Name)

		if i.Labels == nil {
			i.Labels = make(map[string]string)
		}

		i.Labels[ImagePinnedLabel] = "pinned"
		if err := AddToImageSources(&i, bundlePath, modtime); err != nil {
			return fmt.Errorf("failed to add image source: %w", err)
		}

		if _, err := isvc.Update(ctx, i, fieldpaths...); err != nil {
			return fmt.Errorf("failed to add labels for image %s: %w", i.Name, err)
		}
	}
	return nil
}

func (a *OCIBundleReconciler) Stop() error {
	if stop := a.stop; stop != nil {
		stop()
	}
	return nil
}

// ImageSources holds a map of bundle paths with their respective modification times.
// this is used to track from which bundles a given image was imported.
type ImageSources map[string]time.Time

// Refresh removes from the list of source paths all the paths that no longer exists
// or have been modified.
func (i *ImageSources) Refresh() error {
	newmap := map[string]time.Time{}
	for path, modtime := range *i {
		finfo, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("failed to stat %s: %w", path, err)
		}
		if finfo.ModTime().Equal(modtime) {
			newmap[path] = modtime
		}
	}
	*i = newmap
	return nil
}

// Exist returns true if a given bundle source file still exists in the node fs.
func (i *ImageSources) Exist() (bool, error) {
	for path, modtime := range *i {
		finfo, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return false, fmt.Errorf("failed to stat %s: %w", path, err)
		}
		if finfo.ModTime().Equal(modtime) {
			return true, nil
		}
	}
	return false, nil
}

// GetImageSources parses the image source label and returns the ImageSources. if
// no label has been set in the image this returns an empty but initiated map.
func GetImageSources(image images.Image) (ImageSources, error) {
	paths := map[string]time.Time{}
	value, found := image.Labels[ImageSourcePathsLabel]
	if !found {
		return paths, nil
	}
	if err := json.Unmarshal([]byte(value), &paths); err != nil {
		return nil, fmt.Errorf("failed to unmarshal label: %w", err)
	}
	return paths, nil
}

// SetImageSources sets the image source label in the image. this function will
// trim out of the sources the ones that no longer exists in the node fs.
func SetImageSources(image *images.Image, sources ImageSources) error {
	if len(sources) == 0 {
		return nil
	}
	data, err := json.Marshal(sources)
	if err != nil {
		return fmt.Errorf("failed to marshal image source: %w", err)
	}
	if image.Labels == nil {
		image.Labels = map[string]string{}
	}
	image.Labels[ImageSourcePathsLabel] = string(data)
	return nil
}

// AddToImageSources adds a new source path to the image sources. this function
// will trim out of the sources the ones that no longer exists in the node fs.
func AddToImageSources(image *images.Image, path string, modtime time.Time) error {
	paths, err := GetImageSources(*image)
	if err != nil {
		return fmt.Errorf("failed to get image sources: %w", err)
	}
	if err := paths.Refresh(); err != nil {
		return fmt.Errorf("failed to refresh image sources: %w", err)
	}
	paths[path] = modtime
	return SetImageSources(image, paths)
}
