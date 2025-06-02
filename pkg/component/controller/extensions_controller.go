/*
Copyright 2021 k0s authors

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

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/templatewriter"
	helmv1beta1 "github.com/k0sproject/k0s/pkg/apis/helm/v1beta1"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	helmclient "github.com/k0sproject/k0s/pkg/client/clientset/typed/helm/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/controller/leaderelector"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/helm"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"
	"github.com/k0sproject/k0s/pkg/leaderelection"
	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	apiretry "k8s.io/client-go/util/retry"
)

// Helm watch for Chart crd
type ExtensionsController struct {
	L             *logrus.Entry
	helm          *helm.Commands
	charts        helmclient.ChartInterface
	leaderElector leaderelector.Interface
	manifestsDir  string
	stop          context.CancelFunc
}

var _ manager.Component = (*ExtensionsController)(nil)
var _ manager.Reconciler = (*ExtensionsController)(nil)

// NewExtensionsController builds new HelmAddons
func NewExtensionsController(k0sVars *config.CfgVars, kubeClientFactory kubeutil.ClientFactoryInterface, leaderElector leaderelector.Interface) (*ExtensionsController, error) {
	k0sClient, err := kubeClientFactory.GetK0sClient()
	if err != nil {
		return nil, fmt.Errorf("failed to get k0s client: %w", err)
	}
	charts := k0sClient.HelmV1beta1().Charts(metav1.NamespaceSystem)

	return &ExtensionsController{
		L:             logrus.WithFields(logrus.Fields{"component": "extensions_controller"}),
		helm:          helm.NewCommands(k0sVars),
		charts:        charts,
		leaderElector: leaderElector,
		manifestsDir:  filepath.Join(k0sVars.ManifestsDir, "helm"),
	}, nil
}

// Run runs the extensions controller
func (ec *ExtensionsController) Reconcile(ctx context.Context, clusterConfig *k0sv1beta1.ClusterConfig) error {
	ec.L.Info("Extensions reconciliation started")
	defer ec.L.Info("Extensions reconciliation finished")
	return ec.reconcileHelmExtensions(clusterConfig.Spec.Extensions.Helm)
}

// reconcileHelmExtensions creates instance of Chart CR for each chart of the config file
// it also reconciles repositories settings
// the actual helm install/update/delete management is done by ChartReconciler structure
func (ec *ExtensionsController) reconcileHelmExtensions(helmSpec *k0sv1beta1.HelmExtensions) error {
	if helmSpec == nil {
		return nil
	}

	var errs []error
	for _, repo := range helmSpec.Repositories {
		if err := ec.addRepo(repo); err != nil {
			errs = append(errs, fmt.Errorf("can't init repository %q: %w", repo.URL, err))
		}
	}

	var fileNamesToKeep []string
	for _, chart := range helmSpec.Charts {
		fileName := chartManifestFileName(&chart)
		fileNamesToKeep = append(fileNamesToKeep, fileName)

		path, err := ec.writeChartManifestFile(&chart, fileName)
		if err != nil {
			errs = append(errs, fmt.Errorf("can't write file for Helm chart manifest %q: %w", chart.ChartName, err))
			continue
		}

		ec.L.Infof("Wrote Helm chart manifest file %q", path)
	}

	if err := filepath.WalkDir(ec.manifestsDir, func(path string, entry fs.DirEntry, err error) error {
		switch {
		case !entry.Type().IsRegular():
			ec.L.Debugf("Keeping %v as it is not a regular file", entry)
		case slices.Contains(fileNamesToKeep, entry.Name()):
			ec.L.Debugf("Keeping %v as it belongs to a known Helm extension", entry)
		case !isChartManifestFileName(entry.Name()):
			ec.L.Debugf("Keeping %v as it is not a Helm chart manifest file", entry)
		default:
			if err := os.Remove(path); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, fmt.Errorf("failed to remove Helm chart manifest file, the Chart resource will remain in the cluster: %w", err))
				}
			} else {
				ec.L.Infof("Removed Helm chart manifest file %q", path)
			}
		}

		return nil
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to walk Helm chart manifest directory: %w", err))
	}

	return errors.Join(errs...)
}

func (ec *ExtensionsController) writeChartManifestFile(chart *k0sv1beta1.Chart, fileName string) (string, error) {
	tw := templatewriter.TemplateWriter{
		Path:     filepath.Join(ec.manifestsDir, fileName),
		Name:     "addon_crd_manifest",
		Template: chartCrdTemplate,
		Data: struct {
			*k0sv1beta1.Chart
			Finalizer string
		}{
			Chart:     chart,
			Finalizer: finalizerName,
		},
	}
	if err := tw.Write(); err != nil {
		return "", err
	}
	return tw.Path, nil
}

// Determines the file name to use when storing a chart as a manifest on disk.
func chartManifestFileName(c *k0sv1beta1.Chart) string {
	return fmt.Sprintf("%d_helm_extension_%s.yaml", c.Order, c.Name)
}

// Determines if the given file name is in the format for chart manifest file names.
func isChartManifestFileName(fileName string) bool {
	return regexp.MustCompile(`^-?[0-9]+_helm_extension_.+\.yaml$`).MatchString(fileName)
}

func (cr *ExtensionsController) reconcile(ctx context.Context, chartInstance *helmv1beta1.Chart) error {
	if !chartInstance.DeletionTimestamp.IsZero() {
		cr.L.Debug("Uninstall chart ", chartInstance.Name)
		// uninstall chart
		if err := cr.uninstall(ctx, chartInstance); err != nil {
			if !errors.Is(err, driver.ErrReleaseNotFound) {
				return fmt.Errorf("can't uninstall chart: %w", err)
			}

			cr.L.Debugf("No Helm release found for chart %s, assuming it has already been uninstalled", chartInstance.Name)
		}

		if err := removeFinalizer(ctx, cr.charts, chartInstance); err != nil {
			return fmt.Errorf("while trying to remove finalizer: %w", err)
		}

		return nil
	}
	cr.L.Debug("Install or update chart ", chartInstance.Name)
	if err := cr.updateOrInstallChart(ctx, chartInstance); err != nil {
		return fmt.Errorf("can't update or install chart: %w", err)
	}

	cr.L.Debug("Installed or updated chart ", chartInstance.Name)
	return nil
}

func (cr *ExtensionsController) uninstall(ctx context.Context, chart *helmv1beta1.Chart) error {
	if err := cr.helm.UninstallRelease(ctx, chart.Status.ReleaseName, chart.Status.Namespace); err != nil {
		return fmt.Errorf("can't uninstall release `%s/%s`: %w", chart.Status.Namespace, chart.Status.ReleaseName, err)
	}
	return nil
}

func removeFinalizer(ctx context.Context, c helmclient.ChartInterface, chart *helmv1beta1.Chart) error {
	idx := slices.Index(chart.Finalizers, finalizerName)
	if idx < 0 {
		return nil
	}

	path := fmt.Sprintf("/metadata/finalizers/%d", idx)
	patch, err := json.Marshal([]struct {
		Op    string `json:"op"`
		Path  string `json:"path"`
		Value string `json:"value,omitempty"`
	}{
		{"test", path, finalizerName},
		{"remove", path, ""},
	})
	if err != nil {
		return err
	}

	_, err = c.Patch(ctx, chart.Name, types.JSONPatchType, patch, metav1.PatchOptions{
		FieldManager: "k0s",
	})

	return err
}

const defaultTimeout = 10 * time.Minute

func (cr *ExtensionsController) updateOrInstallChart(ctx context.Context, chart *helmv1beta1.Chart) error {
	var err error
	var chartRelease *release.Release
	timeout, err := time.ParseDuration(chart.Spec.Timeout)
	if err != nil {
		cr.L.Tracef("Can't parse `%s` as time.Duration, using default timeout `%s`", chart.Spec.Timeout, defaultTimeout)
		timeout = defaultTimeout
	}
	if timeout == 0 {
		cr.L.Tracef("Using default timeout `%s`, failed to parse `%s`", defaultTimeout, chart.Spec.Timeout)
		timeout = defaultTimeout
	}
	defer func() {
		if err == nil {
			return
		}
		if err := apiretry.RetryOnConflict(apiretry.DefaultRetry, func() error {
			return cr.updateStatus(ctx, chart, chartRelease, err)
		}); err != nil {
			cr.L.WithError(err).Error("Failed to update status for chart release, give up", chart.Name)
		}
	}()
	if chart.Status.ReleaseName == "" {
		// new chartRelease
		cr.L.Tracef("Start update or install %s", chart.Spec.ChartName)
		chartRelease, err = cr.helm.InstallChart(ctx,
			chart.Spec.ChartName,
			chart.Spec.Version,
			chart.Spec.ReleaseName,
			chart.Spec.Namespace,
			chart.Spec.YamlValues(),
			timeout,
		)
		if err != nil {
			return fmt.Errorf("can't reconcile installation for %q: %w", chart.GetName(), err)
		}
	} else {
		if !chartNeedsUpgrade(chart) {
			return nil
		}
		// update
		chartRelease, err = cr.helm.UpgradeChart(ctx,
			chart.Spec.ChartName,
			chart.Spec.Version,
			chart.Status.ReleaseName,
			chart.Status.Namespace,
			chart.Spec.YamlValues(),
			timeout,
			chart.Spec.ShouldForceUpgrade(),
		)
		if err != nil {
			return fmt.Errorf("can't reconcile upgrade for %q: %w", chart.GetName(), err)
		}
	}
	if err := apiretry.RetryOnConflict(apiretry.DefaultRetry, func() error {
		return cr.updateStatus(ctx, chart, chartRelease, nil)
	}); err != nil {
		cr.L.WithError(err).Error("Failed to update status for chart release, give up", chart.Name)
	}
	return nil
}

func chartNeedsUpgrade(chart *helmv1beta1.Chart) bool {
	return chart.Status.Namespace != chart.Spec.Namespace ||
		chart.Status.ReleaseName != chart.Spec.ReleaseName ||
		chart.Status.Version != chart.Spec.Version ||
		chart.Status.ValuesHash != chart.Spec.HashValues()
}

// updateStatus updates the status of the chart with the given release information. This function
// starts by fetching an updated version of the chart from the api as the install may take a while
// to complete and the chart may have been updated in the meantime. If returns the error returned
// by the Update operation. Moreover, if the chart has indeed changed in the meantime we already
// have an event for it so we will see it again soon.
func (cr *ExtensionsController) updateStatus(ctx context.Context, chart *helmv1beta1.Chart, chartRelease *release.Release, err error) error {
	var updchart *helmv1beta1.Chart
	if chart, err := cr.charts.Get(ctx, chart.Name, metav1.GetOptions{}); err != nil {
		return fmt.Errorf("can't get updated version of chart: %w", err)
	} else {
		updchart = chart
	}
	chart.Spec.YamlValues() // XXX what is this function for ?
	if chartRelease != nil {
		updchart.Status.ReleaseName = chartRelease.Name
		updchart.Status.Version = chartRelease.Chart.Metadata.Version
		updchart.Status.AppVersion = chartRelease.Chart.AppVersion()
		updchart.Status.Revision = int64(chartRelease.Version)
		updchart.Status.Namespace = chartRelease.Namespace
	}
	updchart.Status.Updated = time.Now().String()
	updchart.Status.Error = ""
	if err != nil {
		updchart.Status.Error = err.Error()
	}
	updchart.Status.ValuesHash = chart.Spec.HashValues()
	if _, err := cr.charts.UpdateStatus(ctx, updchart, metav1.UpdateOptions{FieldManager: "k0s"}); err != nil {
		cr.L.WithError(err).Error("Failed to update status for chart release", chart.Name)
		return err
	}
	return nil
}

func (ec *ExtensionsController) addRepo(repo k0sv1beta1.Repository) error {
	return ec.helm.AddRepository(repo)
}

const chartCrdTemplate = `
apiVersion: helm.k0sproject.io/v1beta1
kind: Chart
metadata:
  name: k0s-addon-chart-{{ .Name }}
  namespace: "kube-system"
  finalizers:
    - {{ .Finalizer }}
spec:
  chartName: {{ .ChartName }}
  releaseName: {{ .Name }}
  timeout: {{ .Timeout.Duration }}
  values: |
{{ .Values | nindent 4 }}
  version: {{ .Version }}
  namespace: {{ .TargetNS }}
{{- if ne .ForceUpgrade nil }}
  forceUpgrade: {{ .ForceUpgrade }}
{{- end }}
`

const finalizerName = "helm.k0sproject.io/uninstall-helm-release"

// Init
func (ec *ExtensionsController) Init(_ context.Context) error {
	return nil
}

// Start
func (ec *ExtensionsController) Start(ctx context.Context) error {
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())

	wg.Add(1)
	go func() {
		defer wg.Done()

		leaderelection.RunLeaderTasks(ctx, ec.leaderElector.CurrentStatus, func(ctx context.Context) {
			var queue workQueue[*helmv1beta1.Chart]

			wg.Add(1)
			go func() {
				defer wg.Done()
				ec.watchCharts(ctx, &queue)
			}()

			ec.drainQueue(ctx, &queue)
		})
	}()

	ec.stop = func() {
		cancel()
		wg.Wait()
	}

	return nil
}

func (ec *ExtensionsController) watchCharts(ctx context.Context, queue *workQueue[*helmv1beta1.Chart]) {
	wait.UntilWithContext(ctx, func(ctx context.Context) {
		if err := watch.Charts(ec.charts).
			IncludingDeletions().
			WithErrorCallback(func(err error) (time.Duration, error) {
				if retryDelay, e := watch.IsRetryable(err); e == nil {
					ec.L.WithError(err).Debugf("Encountered transient error while watching charts, retrying in %s", retryDelay)
					return retryDelay, nil
				}
				return 0, err
			}).
			Until(ctx, func(chart *helmv1beta1.Chart) (bool, error) {
				queue.enqueue(chart, time.Now(), 0)
				return false, nil
			}); !errors.Is(err, ctx.Err()) {
			ec.L.WithError(err).Error("Failed to watch charts")
		}
	}, 10*time.Second)
}

func (ec *ExtensionsController) drainQueue(ctx context.Context, queue *workQueue[*helmv1beta1.Chart]) {
	nextTimer := time.NewTimer(0)
	defer nextTimer.Stop()

	for {
		next, queueUpdated := queue.peekOrPop()

		// If queueUpdated isn't nil, we need to wait.
		if queueUpdated != nil {
			var nextTimeout <-chan time.Time
			if next != nil {
				nextTimer.Reset(time.Until(next.deadline))
				nextTimeout = nextTimer.C
			}

			select {
			case <-queueUpdated:
				continue
			case <-nextTimeout:
				continue
			case <-ctx.Done():
				return
			}
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		chart := next.inner
		// FIXME what context to use here? Shall we cancel it on lost leases?
		if err := ec.reconcile(ctx, chart); err != nil {
			if next.failureCount == 9 {
				ec.L.WithError(err).Error("Failed to reconcile chart ", chart.Name, ", giving up after 10 attempts")
			} else {
				next.failureCount++
				timeout := time.Duration(next.failureCount) * time.Minute
				ec.L.WithError(err).Error("Failed to reconcile chart ", chart.Name, ", retrying in ", timeout.String())
				queue.enqueue(chart, time.Now().Add(timeout), next.failureCount)
			}
		}
	}
}

// Stop
func (ec *ExtensionsController) Stop() error {
	if ec.stop != nil {
		ec.stop()
	}
	ec.L.Debug("Stopped extensions controller")
	return nil
}

type workQueueItem[T any] struct {
	inner        T
	failureCount uint
	deadline     time.Time
}

type workQueue[R metav1.Object] struct {
	mu    sync.Mutex
	next  *workQueueItem[R]
	items map[string]*workQueueItem[R]
	ch    chan struct{}
}

func (w *workQueue[R]) peekOrPop() (*workQueueItem[R], <-chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Find the new next chart to pop, if necessary.
	if w.next == nil {
		for _, chart := range w.items {
			if w.next == nil || chart.deadline.Before(w.next.deadline) {
				w.next = chart
			}
		}
	}

	// Pop the next chart if it's time to do so.
	if w.next != nil && !w.next.deadline.After(time.Now()) {
		popped := w.next
		w.next = nil
		delete(w.items, popped.inner.GetName())
		if w.ch != nil {
			close(w.ch)
			w.ch = nil
		}
		return popped, nil
	}

	// Either the queue is empty or the next item still needs to wait.
	ch := w.ch
	if ch == nil {
		ch = make(chan struct{})
		w.ch = ch
	}

	return w.next, ch
}

func (w *workQueue[R]) enqueue(resource R, deadline time.Time, failureCount uint) {
	w.mu.Lock()
	defer w.mu.Unlock()

	name := resource.GetName()
	item := w.items[name]
	if item == nil {
		item = &workQueueItem[R]{resource, failureCount, deadline}
		if w.items == nil {
			w.items = make(map[string]*workQueueItem[R])
		}
		w.items[name] = item
	} else {
		if failureCount > 0 {
			item.failureCount = failureCount
		} else {
			item.inner = resource
		}
		if !deadline.Before(item.deadline) {
			return
		}
		item.deadline = deadline
	}

	if w.next != nil {
		// Return early if the next resource didn't change.
		if item != w.next && !item.deadline.Before(w.next.deadline) {
			return
		}
		w.next = item
	}

	// Notify any waiters about the updated queue.
	if w.ch != nil {
		close(w.ch)
		w.ch = nil
	}
}
