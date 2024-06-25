package applier

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/k0sproject/k0s/pkg/component/controller/leaderelector"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestManager_Rename(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.InfoLevel)
	cf := testutil.NewFakeClientFactory()
	manifestsDir := t.TempDir()
	leaderElector := leaderelector.Dummy{}

	underTest := Manager{
		ManifestsDir:      manifestsDir,
		KubeClientFactory: cf,
		LeaderElector:     &leaderElector,
		log:               log.WithField("test", t.Name()),
	}

	require.NoError(t, underTest.Init(context.TODO()))
	require.NoError(t, underTest.Start(context.TODO()))
	defer func() { assert.NoError(t, underTest.Stop()) }()
	leaderElector.Leader = true
	require.NoError(t, leaderElector.Start(context.TODO()))

	theMap, err := yaml.Marshal(&map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "the-map"},
	})
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(manifestsDir, "lorem"), 0755))
	require.NoError(t, file.AtomicWithTarget(filepath.Join(manifestsDir, "lorem", "the-map.yaml")).Write(theMap))
	t.Log("Waiting for stack to be applied")
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		configMaps, err := cf.Client.CoreV1().ConfigMaps(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
		if assert.NoError(t, err) && assert.Len(t, configMaps.Items, 1) {
			cm := &configMaps.Items[0]
			assert.Equal(t, cm.Name, "the-map")
			assert.Subset(t, cm.Labels, map[string]string{"k0s.k0sproject.io/stack": "lorem"})
		}
	}, 20*time.Second, 350*time.Millisecond)

	require.NoError(t, os.Rename(filepath.Join(manifestsDir, "lorem"), filepath.Join(manifestsDir, "ipsum")))
	t.Log("Waiting for stack to be changed")
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		configMaps, err := cf.Client.CoreV1().ConfigMaps(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
		if assert.NoError(t, err) && assert.Len(t, configMaps.Items, 1) {
			cm := &configMaps.Items[0]
			assert.Equal(t, cm.Name, "the-map")
			assert.Subset(t, cm.Labels, map[string]string{"k0s.k0sproject.io/stack": "ipsum"})
		}
	}, 20*time.Second, 350*time.Millisecond)

	require.NoError(t, os.Rename(filepath.Join(manifestsDir, "ipsum"), filepath.Join(t.TempDir(), "moved-away")))
	t.Log("Waiting for stack to be deleted")
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		configMaps, err := cf.Client.CoreV1().ConfigMaps(metav1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
		if assert.NoError(t, err) {
			assert.Empty(t, configMaps.Items)
		}
	}, 20*time.Second, 350*time.Millisecond)

}
