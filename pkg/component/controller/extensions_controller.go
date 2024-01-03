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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/avast/retry-go"
	"github.com/bombsimon/logrusr/v4"
	"github.com/k0sproject/k0s/internal/pkg/templatewriter"
	helmapi "github.com/k0sproject/k0s/pkg/apis/helm"
	"github.com/k0sproject/k0s/pkg/apis/helm/v1beta1"
	k0sAPI "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/controller/leaderelector"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/helm"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	crman "sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
)

// Helm watch for Chart crd
type ExtensionsController struct {
	saver         manifestsSaver
	L             *logrus.Entry
	helm          *helm.Commands
	kubeConfig    string
	leaderElector leaderelector.Interface
}

var _ manager.Component = (*ExtensionsController)(nil)
var _ manager.Reconciler = (*ExtensionsController)(nil)

// NewExtensionsController builds new HelmAddons
func NewExtensionsController(s manifestsSaver, k0sVars *config.CfgVars, kubeClientFactory kubeutil.ClientFactoryInterface, leaderElector leaderelector.Interface) *ExtensionsController {
	return &ExtensionsController{
		saver:         s,
		L:             logrus.WithFields(logrus.Fields{"component": "extensions_controller"}),
		helm:          helm.NewCommands(k0sVars),
		kubeConfig:    k0sVars.AdminKubeConfigPath,
		leaderElector: leaderElector,
	}
}

const (
	namespaceToWatch = "kube-system"
)

// Run runs the extensions controller
func (ec *ExtensionsController) Reconcile(ctx context.Context, clusterConfig *k0sAPI.ClusterConfig) error {
	ec.L.Info("Extensions reconciliation started")
	defer ec.L.Info("Extensions reconciliation finished")

	helmSettings := clusterConfig.Spec.Extensions.Helm
	var err error
	switch clusterConfig.Spec.Extensions.Storage.Type {
	case k0sAPI.OpenEBSLocal:
		helmSettings, err = addOpenEBSHelmExtension(helmSettings, clusterConfig.Spec.Extensions.Storage)
		if err != nil {
			ec.L.WithError(err).Error("Can't add openebs helm extension")
		}
	default:
	}

	if err := ec.reconcileHelmExtensions(helmSettings); err != nil {
		return fmt.Errorf("can't reconcile helm based extensions: %w", err)
	}

	return nil
}

func addOpenEBSHelmExtension(helmSpec *k0sAPI.HelmExtensions, storageExtension *k0sAPI.StorageExtension) (*k0sAPI.HelmExtensions, error) {
	openEBSValues := map[string]interface{}{
		"localprovisioner": map[string]interface{}{
			"hostpathClass": map[string]interface{}{
				"enabled":        true,
				"isDefaultClass": storageExtension.CreateDefaultStorageClass,
			},
		},
	}
	values, err := yamlifyValues(openEBSValues)
	if err != nil {
		logrus.Errorf("can't yamlify openebs values: %v", err)
		return nil, err
	}
	if helmSpec == nil {
		helmSpec = &k0sAPI.HelmExtensions{
			Repositories: k0sAPI.RepositoriesSettings{},
			Charts:       k0sAPI.ChartsSettings{},
		}
	}
	helmSpec.Repositories = append(helmSpec.Repositories, k0sAPI.Repository{
		Name: "openebs-internal",
		URL:  constant.OpenEBSRepository,
	})
	helmSpec.Charts = append(helmSpec.Charts, k0sAPI.Chart{
		Name:      "openebs",
		ChartName: "openebs-internal/openebs",
		TargetNS:  "openebs",
		Version:   constant.OpenEBSVersion,
		Values:    values,
		Timeout:   time.Duration(time.Minute * 30), // it takes a while to install openebs
	})
	return helmSpec, nil
}

func yamlifyValues(values map[string]interface{}) (string, error) {
	bytes, err := yaml.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// reconcileHelmExtensions creates instance of Chart CR for each chart of the config file
// it also reconciles repositories settings
// the actual helm install/update/delete management is done by ChartReconciler structure
func (ec *ExtensionsController) reconcileHelmExtensions(helmSpec *k0sAPI.HelmExtensions) error {
	if helmSpec == nil {
		return nil
	}

	for _, repo := range helmSpec.Repositories {
		if err := ec.addRepo(repo); err != nil {
			return fmt.Errorf("can't init repository %q: %w", repo.URL, err)
		}
	}

	for _, chart := range helmSpec.Charts {
		tw := templatewriter.TemplateWriter{
			Name:     "addon_crd_manifest",
			Template: chartCrdTemplate,
			Data: struct {
				k0sAPI.Chart
				Finalizer string
			}{
				Chart:     chart,
				Finalizer: finalizerName,
			},
		}
		buf := bytes.NewBuffer([]byte{})
		if err := tw.WriteToBuffer(buf); err != nil {
			return fmt.Errorf("can't create chart CR instance %q: %w", chart.ChartName, err)
		}
		if err := ec.saver.Save(chart.ManifestFileName(), buf.Bytes()); err != nil {
			return fmt.Errorf("can't save addon CRD manifest for chart CR instance %q: %w", chart.ChartName, err)
		}
	}
	return nil
}

type ChartReconciler struct {
	client.Client
	helm          *helm.Commands
	leaderElector leaderelector.Interface
	L             *logrus.Entry
}

func (cr *ChartReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	if !cr.leaderElector.IsLeader() {
		return reconcile.Result{}, nil
	}

	var chart v1beta1.Chart
	if err := cr.Client.Get(ctx, req.NamespacedName, &chart); err != nil {
		if apierrors.IsNotFound(err) {
			cr.L.WithField("chart", req.NamespacedName).WithError(err).Debug("Chart not found")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("while getting chart: %w", err)
	}

	switch {
	case !chart.DeletionTimestamp.IsZero():
		return reconcile.Result{}, cr.uninstallAndDeleteChart(ctx, &chart)

	case chart.Status.ReleaseName == "":
		return reconcile.Result{}, cr.installChart(ctx, &chart)

	case cr.chartNeedsUpgrade(&chart):
		return reconcile.Result{}, cr.upgradeChart(ctx, &chart)

	default:
		cr.L.WithField("chart", req.NamespacedName).Debug("Chart is up to date")
		return reconcile.Result{}, nil
	}
}

func (cr *ChartReconciler) uninstallAndDeleteChart(ctx context.Context, chart *v1beta1.Chart) error {
	log := cr.L.
		WithField("chart", types.NamespacedName{Namespace: chart.Namespace, Name: chart.Name}).
		WithField("release", fmt.Sprintf("%s/%s", chart.Status.Namespace, chart.Status.ReleaseName))

	timeout := cr.getTimeout(ctx, chart)
	log.Info("Uninstalling release, timeout is ", timeout)
	if err := cr.helm.UninstallRelease(chart.Status.ReleaseName, chart.Status.Namespace, timeout); err != nil {
		if !errors.Is(err, driver.ErrReleaseNotFound) {
			cr.updateStatus(ctx, chart, nil, err)
			return fmt.Errorf("while uninstalling release `%s/%s`: %w", chart.Status.Namespace, chart.Status.ReleaseName, err)
		}

		log.Info("Release not found, assuming it has already been uninstalled")
	} else {
		log.Info("Release has been uninstalled")
		var zeroRelease release.Release
		cr.updateStatus(ctx, chart, &zeroRelease, nil)
	}

	if err := removeFinalizer(ctx, cr.Client, chart); err != nil {
		return fmt.Errorf("while trying to remove finalizer: %w", err)
	}

	log.Info("Chart has been uninstalled")
	return nil
}

func (cr *ChartReconciler) getTimeout(ctx context.Context, chart *v1beta1.Chart) time.Duration {
	const defaultTimeout = 10 * time.Minute

	timeout := defaultTimeout
	if chartTimeout, err := time.ParseDuration(chart.Spec.Timeout); err != nil {
		cr.L.
			WithField("chart", types.NamespacedName{Namespace: chart.Namespace, Name: chart.Name}).
			WithError(err).
			Warnf("Failed to parse timeout: %q", chart.Spec.Timeout)
	} else {
		timeout = chartTimeout
	}
	if deadline, ok := ctx.Deadline(); ok {
		ctxTimeout := time.Until(deadline)
		if ctxTimeout < timeout {
			timeout = ctxTimeout
		}
	}

	return timeout
}

func removeFinalizer(ctx context.Context, c client.Client, chart *v1beta1.Chart) error {
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

	return c.Patch(ctx, chart, client.RawPatch(types.JSONPatchType, patch))
}

func (cr *ChartReconciler) installChart(ctx context.Context, chart *v1beta1.Chart) error {
	log := cr.L.
		WithField("chart", types.NamespacedName{Namespace: chart.Namespace, Name: chart.Name}).
		WithField("release", fmt.Sprintf("%s/%s", chart.Spec.Namespace, chart.Spec.ReleaseName))

	timeout := cr.getTimeout(ctx, chart)
	log.Info("Installing chart, timeout is ", timeout)
	chartRelease, err := cr.helm.InstallChart(ctx,
		chart.Spec.ChartName,
		chart.Spec.Version,
		chart.Spec.ReleaseName,
		chart.Spec.Namespace,
		chart.Spec.YamlValues(),
		timeout,
	)
	if err != nil {
		cr.updateStatus(ctx, chart, nil, err)
		return fmt.Errorf("can't reconcile installation for %q: %w", chart.GetName(), err)
	}

	log.Info("Chart has been installed: ", chartRelease)
	cr.updateStatus(ctx, chart, chartRelease, nil)
	return nil
}

func (cr *ChartReconciler) upgradeChart(ctx context.Context, chart *v1beta1.Chart) error {
	log := cr.L.
		WithField("chart", types.NamespacedName{Namespace: chart.Namespace, Name: chart.Name}).
		WithField("release", fmt.Sprintf("%s/%s", chart.Status.Namespace, chart.Status.ReleaseName))

	timeout := cr.getTimeout(ctx, chart)
	log.Info("Upgrading chart, timeout is ", timeout)
	chartRelease, err := cr.helm.UpgradeChart(ctx,
		chart.Spec.ChartName,
		chart.Spec.Version,
		chart.Status.ReleaseName,
		chart.Status.Namespace,
		chart.Spec.YamlValues(),
		timeout,
	)
	if err != nil {
		cr.updateStatus(ctx, chart, nil, err)
		return fmt.Errorf("can't reconcile upgrade for %q: %w", chart.GetName(), err)
	}

	log.Info("Chart has been upgraded: ", chartRelease)
	cr.updateStatus(ctx, chart, chartRelease, nil)
	return nil
}

func (cr *ChartReconciler) chartNeedsUpgrade(chart *v1beta1.Chart) bool {
	return !(chart.Status.Namespace == chart.Spec.Namespace &&
		chart.Status.ReleaseName == chart.Spec.ReleaseName &&
		chart.Status.Version == chart.Spec.Version &&
		chart.Status.ValuesHash == chart.Spec.HashValues())
}

func (cr *ChartReconciler) updateStatus(ctx context.Context, chart *v1beta1.Chart, chartRelease *release.Release, err error) {

	chart.Spec.YamlValues()
	if chartRelease != nil {
		chart.Status.ReleaseName = chartRelease.Name
		chart.Status.Version = chartRelease.Chart.Metadata.Version
		chart.Status.AppVersion = chartRelease.Chart.AppVersion()
		chart.Status.Revision = int64(chartRelease.Version)
		chart.Status.Namespace = chartRelease.Namespace
	}
	chart.Status.Updated = time.Now().String()
	if err != nil {
		chart.Status.Error = err.Error()
	} else {
		chart.Status.Error = ""
	}
	chart.Status.ValuesHash = chart.Spec.HashValues()
	if updErr := cr.Client.Status().Update(ctx, chart); updErr != nil {
		cr.L.
			WithField("chart", types.NamespacedName{Namespace: chart.Namespace, Name: chart.Name}).
			WithError(updErr).
			Error("Failed to update chart status")
	}
}

func (ec *ExtensionsController) addRepo(repo k0sAPI.Repository) error {
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
  timeout: {{ .Timeout }}
  values: |
{{ .Values | nindent 4 }}
  version: {{ .Version }}
  namespace: {{ .TargetNS }}
`

const finalizerName = "helm.k0sproject.io/uninstall-helm-release"

// Init
func (ec *ExtensionsController) Init(_ context.Context) error {
	return nil
}

// Start
func (ec *ExtensionsController) Start(ctx context.Context) error {
	clientConfig, err := clientcmd.BuildConfigFromFlags("", ec.kubeConfig)
	if err != nil {
		return fmt.Errorf("can't build controller-runtime controller for helm extensions: %w", err)
	}
	gk := schema.GroupKind{
		Group: helmapi.GroupName,
		Kind:  "Chart",
	}

	mgr, err := crman.New(clientConfig, crman.Options{
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		Logger:     logrusr.New(ec.L),
		Controller: ctrlconfig.Controller{},
	})
	if err != nil {
		return fmt.Errorf("can't build controller-runtime controller for helm extensions: %w", err)
	}
	if err := retry.Do(func() error {
		_, err := mgr.GetRESTMapper().RESTMapping(gk)
		if err != nil {
			ec.L.Warn("Extensions CRD is not yet ready, waiting before starting ExtensionsController")
			return err
		}
		ec.L.Info("Extensions CRD is ready, going nuts")
		return nil
	}, retry.Context(ctx)); err != nil {
		return fmt.Errorf("can't start ExtensionsReconciler, helm CRD is not registred, check CRD registration reconciler: %w", err)
	}
	// examples say to not use GetScheme in production, but it is unclear at the moment
	// which scheme should be in use
	if err := v1beta1.AddToScheme(mgr.GetScheme()); err != nil {
		return fmt.Errorf("can't register Chart crd: %w", err)
	}

	if err := builder.
		ControllerManagedBy(mgr).
		For(&v1beta1.Chart{},
			builder.WithPredicates(predicate.And(
				predicate.GenerationChangedPredicate{},
				predicate.NewPredicateFuncs(func(object client.Object) bool {
					return object.GetNamespace() == namespaceToWatch
				}),
			),
			),
		).
		Complete(&ChartReconciler{
			Client:        mgr.GetClient(),
			leaderElector: ec.leaderElector, // TODO: drop in favor of controller-runtime lease manager?
			helm:          ec.helm,
			L:             ec.L.WithField("extensions_type", "helm"),
		}); err != nil {
		return fmt.Errorf("can't build controller-runtime controller for helm extensions: %w", err)
	}

	go func() {
		if err := mgr.Start(ctx); err != nil {
			ec.L.WithError(err).Error("Controller manager working loop exited")
		}
	}()

	return nil
}

// Stop
func (ec *ExtensionsController) Stop() error {
	return nil
}
