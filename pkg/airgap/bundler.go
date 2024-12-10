/*
Copyright 2024 k0s authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package airgap

import (
	"archive/tar"
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"path"
	"runtime"
	"slices"
	"sync"

	"github.com/k0sproject/k0s/internal/pkg/stringslice"

	"github.com/distribution/reference"
	"github.com/dustin/go-humanize"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

type InsecureRegistryKind uint8

const (
	NoInsecureRegistry InsecureRegistryKind = iota
	SkipTLSVerifyRegistry
	PlainHTTPRegistry
)

type ImageBundler struct {
	Log                   logrus.FieldLogger
	InsecureRegistries    InsecureRegistryKind
	RegistriesConfigPaths []string
}

func (b *ImageBundler) Run(ctx context.Context, refs []reference.Named, out io.Writer) error {
	var client *http.Client
	if len := len(refs); len < 1 {
		b.Log.Warn("No images to bundle")
	} else {
		b.Log.Infof("About to bundle %d images", len)
		var close func()
		client, close = newClient(b.InsecureRegistries == SkipTLSVerifyRegistry)
		defer close()
	}

	creds, err := newCredentials(b.RegistriesConfigPaths)
	if err != nil {
		return err
	}

	newSource := func(ref reference.Named) oras.ReadOnlyTarget {
		return &remote.Repository{
			Client: &auth.Client{
				Client:     client,
				Credential: creds,
			},
			Reference: registry.Reference{
				Registry:   reference.Domain(ref),
				Repository: reference.Path(ref),
			},
			PlainHTTP: b.InsecureRegistries == PlainHTTPRegistry,
		}
	}

	return bundleImages(ctx, b.Log, newSource, refs, out)
}

func newCredentials(configPaths []string) (_ auth.CredentialFunc, err error) {
	var store credentials.Store
	var opts credentials.StoreOptions

	if len(configPaths) < 1 {
		store, err = credentials.NewStoreFromDocker(opts)
		if err != nil {
			return nil, err
		}
	} else {
		store, err = credentials.NewStore(configPaths[0], opts)
		if err != nil {
			return nil, err
		}
		if configPaths := configPaths[1:]; len(configPaths) > 0 {
			otherStores := make([]credentials.Store, len(configPaths))
			for i, path := range configPaths {
				otherStores[i], err = credentials.NewStore(path, opts)
				if err != nil {
					return nil, err
				}
			}
			store = credentials.NewStoreWithFallbacks(store, otherStores...)
		}
	}

	return credentials.Credential(store), nil
}

func newClient(insecureSkipTLSVerify bool) (_ *http.Client, close func()) {
	// This transports is, by design, a trimmed down version of http's DefaultTransport.
	// No need to have all those timeouts the default client brings in.
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}

	if insecureSkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &http.Client{Transport: transport}, transport.CloseIdleConnections
}

type sourceFactory func(ref reference.Named) oras.ReadOnlyTarget

func bundleImages(ctx context.Context, log logrus.FieldLogger, newSource sourceFactory, refs []reference.Named, out io.Writer) error {
	tarWriter := tar.NewWriter(out)
	target := tarFileTarget{w: &tarBlobWriter{w: tarWriter}}
	index := ocispecv1.Index{
		Versioned: ocispecs.Versioned{SchemaVersion: 2}, // FIXME constant somewhere?
		MediaType: ocispecv1.MediaTypeImageIndex,
	}

	for numRef, ref := range refs {
		targetRefs, err := targetRefsFor(ref)
		if err != nil {
			return err
		}

		log := log.WithFields(logrus.Fields{
			"image": fmt.Sprintf("%d/%d", numRef+1, len(refs)),
			"name":  ref,
		})
		desc, err := bundleImage(ctx, log, newSource, ref, &target)
		if err != nil {
			return err
		}

		// Store the image multiple times with all its possible target refs.
		for _, targetRef := range targetRefs {
			desc := desc // shallow copy
			desc.Annotations = maps.Clone(desc.Annotations)
			if desc.Annotations == nil {
				desc.Annotations = make(map[string]string, 1)
			}
			desc.Annotations[ocispecv1.AnnotationRefName] = targetRef
			index.Manifests = append(index.Manifests, desc)
		}
	}

	if err := writeTarJSON(tarWriter, ocispecv1.ImageIndexFile, index); err != nil {
		return err
	}
	if err := writeTarJSON(tarWriter, ocispecv1.ImageLayoutFile, &ocispecv1.ImageLayout{
		Version: ocispecv1.ImageLayoutVersion,
	}); err != nil {
		return err
	}

	return tarWriter.Close()
}

// Calculates the target references for the given input reference.
func targetRefsFor(ref reference.Named) (targetRefs []string, _ error) {
	// First the name as is
	targetRefs = append(targetRefs, reference.TagNameOnly(ref).String())

	nameOnly := reference.TrimNamed(ref)

	// Then as name:tag
	if tagged, ok := ref.(reference.Tagged); ok {
		tagged, err := reference.WithTag(nameOnly, tagged.Tag())
		if err != nil {
			return nil, err
		}
		targetRefs = append(targetRefs, tagged.String())
	}

	// Then as name@digest
	if digested, ok := ref.(reference.Digested); ok {
		digested, err := reference.WithDigest(nameOnly, digested.Digest())
		if err != nil {
			return nil, err
		}
		targetRefs = append(targetRefs, digested.String())
	}

	// Dedup the refs
	return stringslice.Unique(targetRefs), nil
}

func bundleImage(ctx context.Context, log logrus.FieldLogger, newSource sourceFactory, ref reference.Named, dst oras.Target) (ocispecv1.Descriptor, error) {
	var srcRef string
	if digested, ok := ref.(reference.Digested); ok {
		srcRef = string(digested.Digest())
	} else if tagged, ok := ref.(reference.Tagged); ok {
		srcRef = tagged.Tag()
	}

	var opts oras.CopyOptions
	opts.Concurrency = 1 // reproducible output
	// Implement custom platform filtering. The default opts.WithTargetPlatform
	// will throw away multi-arch image indexes and thus change image digests.
	opts.FindSuccessors = func(ctx context.Context, fetcher content.Fetcher, desc ocispecv1.Descriptor) ([]ocispecv1.Descriptor, error) {
		descs, err := content.Successors(ctx, fetcher, desc)
		if err != nil {
			return nil, err
		}

		var platformDescs []ocispecv1.Descriptor
		for _, desc := range descs {
			p := desc.Platform
			if p != nil && (p.Architecture != runtime.GOARCH || p.OS != runtime.GOOS) {
				continue
			}
			platformDescs = append(platformDescs, desc)
		}

		return platformDescs, nil
	}
	opts.PreCopy = func(ctx context.Context, desc ocispecv1.Descriptor) error {
		log.WithField("digest", desc.Digest).Infof("Copying %s", humanize.IBytes(uint64(desc.Size)))
		return nil
	}

	return oras.Copy(ctx, newSource(ref), srcRef, dst, "", opts)
}

func writeTarDir(w *tar.Writer, name string) error {
	return w.WriteHeader(&tar.Header{
		Name:     name + "/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	})
}

func writeTarJSON(w *tar.Writer, name string, data any) error {
	json, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return writeTarFile(w, name, int64(len(json)), bytes.NewReader(json))
}

func writeTarFile(w *tar.Writer, name string, size int64, in io.Reader) error {
	if err := w.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Mode:     0644,
		Size:     size,
	}); err != nil {
		return err
	}

	_, err := io.Copy(w, in)
	return err
}

type tarFileTarget struct {
	mu sync.RWMutex
	w  *tarBlobWriter
}

type tarBlobWriter struct {
	w     *tar.Writer
	blobs []digest.Digest
}

func (t *tarFileTarget) doLocked(exclusive bool, fn func(w *tarBlobWriter) error) (err error) {
	if exclusive {
		t.mu.Lock()
		defer func() {
			if err != nil {
				t.w = nil
			}
			t.mu.Unlock()
		}()
	} else {
		t.mu.RLock()
		defer t.mu.RUnlock()
	}

	if t.w == nil {
		return errors.New("writer is broken")
	}

	return fn(t.w)
}

// Exists implements [oras.Target].
func (t *tarFileTarget) Exists(ctx context.Context, target ocispecv1.Descriptor) (exists bool, _ error) {
	err := t.doLocked(false, func(w *tarBlobWriter) error {
		_, exists = slices.BinarySearch(w.blobs, target.Digest)
		return nil
	})
	return exists, err
}

// Push implements [oras.Target].
func (t *tarFileTarget) Push(ctx context.Context, expected ocispecv1.Descriptor, in io.Reader) (err error) {
	d := expected.Digest
	if err := d.Validate(); err != nil {
		return err
	}

	lockErr := t.doLocked(true, func(w *tarBlobWriter) error {
		idx, exists := slices.BinarySearch(w.blobs, d)
		if exists {
			err = errdef.ErrAlreadyExists
			return nil
		}

		if len(w.blobs) < 1 {
			if err := writeTarDir(w.w, ocispecv1.ImageBlobsDir); err != nil {
				return err
			}
		}

		if (idx == 0 || w.blobs[idx-1].Algorithm() != d.Algorithm()) &&
			(idx >= len(w.blobs) || w.blobs[idx].Algorithm() != d.Algorithm()) {
			dirName := path.Join(ocispecv1.ImageBlobsDir, d.Algorithm().String())
			if err := writeTarDir(w.w, dirName); err != nil {
				return err
			}
		}

		blobName := path.Join(ocispecv1.ImageBlobsDir, d.Algorithm().String(), d.Hex())
		verify := content.NewVerifyReader(in, expected)
		if err := writeTarFile(w.w, blobName, expected.Size, verify); err != nil {
			return err
		}
		if err := verify.Verify(); err != nil {
			return err
		}

		w.blobs = slices.Insert(w.blobs, idx, d)
		return nil
	})

	return cmp.Or(lockErr, err)
}

// Tag implements [oras.Target].
func (t *tarFileTarget) Tag(ctx context.Context, desc ocispecv1.Descriptor, reference string) error {
	if exists, err := t.Exists(ctx, desc); err != nil {
		return err
	} else if !exists {
		return errdef.ErrNotFound
	}

	return nil // don't store tag information
}

// Resolve implements [oras.Target].
func (t *tarFileTarget) Resolve(ctx context.Context, reference string) (d ocispecv1.Descriptor, _ error) {
	return d, fmt.Errorf("%w: Resolve(_, %q)", errdef.ErrUnsupported, reference)
}

// Fetch implements [oras.Target].
func (t *tarFileTarget) Fetch(ctx context.Context, target ocispecv1.Descriptor) (io.ReadCloser, error) {
	return nil, fmt.Errorf("%w: Fetch(_, %v)", errdef.ErrUnsupported, target)
}
