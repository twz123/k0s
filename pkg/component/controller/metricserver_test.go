// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/k0sproject/k0s/internal/pkg/templatewriter"
	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestGetConfigWithZeroNodes(t *testing.T) {
	cfg := v1beta1.DefaultClusterConfig()
	k0sVars, err := config.NewCfgVars(nil, t.TempDir())
	require.NoError(t, err)
	fakeFactory := testutil.NewFakeClientFactory()
	ctx := t.Context()

	metrics := NewMetricServer(k0sVars, fakeFactory)
	require.NoError(t, metrics.Reconcile(ctx, cfg))
	metricsCfg, err := metrics.getConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "10m", metricsCfg.CPURequest)
	require.Equal(t, "30M", metricsCfg.MEMRequest)
}

func TestGetConfigWithSomeNodes(t *testing.T) {
	cfg := v1beta1.DefaultClusterConfig()
	k0sVars, err := config.NewCfgVars(nil, t.TempDir())
	require.NoError(t, err)
	fakeFactory := testutil.NewFakeClientFactory()
	fakeClient, _ := fakeFactory.GetClient()
	ctx := t.Context()

	for i := 1; i <= 100; i++ {
		n := &corev1.Node{
			ObjectMeta: v1.ObjectMeta{
				Name: fmt.Sprintf("node-%d", i),
			},
		}
		_, err := fakeClient.CoreV1().Nodes().Create(t.Context(), n, v1.CreateOptions{})
		require.NoError(t, err)
	}

	metrics := NewMetricServer(k0sVars, fakeFactory)
	require.NoError(t, metrics.Reconcile(ctx, cfg))
	metricsCfg, err := metrics.getConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "100m", metricsCfg.CPURequest)
	require.Equal(t, "300M", metricsCfg.MEMRequest)
}

func TestMetricServerVerifiesKubelets(t *testing.T) {
	var manifest bytes.Buffer
	tw := templatewriter.TemplateWriter{
		Name:     "metricServer",
		Template: metricServerTemplate,
		Data:     metricsConfig{Image: "example.com/metrics-server:v0", PullPolicy: "Never", CPURequest: "1m", MEMRequest: "1M"},
	}
	require.NoError(t, tw.WriteToBuffer(&manifest))
	resources, err := applier.ReadUnstructuredStream(&manifest, t.Name())
	require.NoError(t, err)

	var deployment appsv1.Deployment
	for _, resource := range resources {
		if resource.GetKind() == "Deployment" {
			require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, &deployment))
		}
	}
	require.NotEmpty(t, deployment.Name, "Expected a Deployment in the manifest")

	pod := deployment.Spec.Template.Spec
	require.Len(t, pod.Containers, 1)
	assert.Contains(t, pod.Containers[0].Args, "--kubelet-certificate-authority=/var/run/k0s/kubelet-serving-ca/ca.crt",
		"Kubelets should be verified against the kubelet-serving CA trust bundle")
	assert.NotContains(t, pod.Containers[0].Args, "--kubelet-insecure-tls", "Kubelets should be verified")

	var mount *corev1.VolumeMount
	for _, m := range pod.Containers[0].VolumeMounts {
		if m.MountPath == "/var/run/k0s/kubelet-serving-ca" {
			mount = &m
		}
	}
	if assert.NotNil(t, mount, "Expected the trust bundle to be mounted") {
		assert.True(t, mount.ReadOnly, "Trust bundle should be mounted read-only")
		var volume *corev1.Volume
		for _, v := range pod.Volumes {
			if v.Name == mount.Name {
				volume = &v
			}
		}
		if assert.NotNil(t, volume, "Expected a volume for the mount") && assert.NotNil(t, volume.ConfigMap, "Expected the volume to be a ConfigMap") {
			assert.Equal(t, "kubelet-serving-ca.crt", volume.ConfigMap.Name, "Volume should be the published trust bundle")
			assert.Nil(t, volume.ConfigMap.Optional, "Trust bundle should be required")
		}
	}
}
