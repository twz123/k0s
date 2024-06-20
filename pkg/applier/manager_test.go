/*
Copyright 2023 k0s authors

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

package applier

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/k0sproject/k0s/pkg/config"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
)

// Create a unit test that verifies that the watchers won't get started when
// the leader elector fires _after_ the component has been stopped.

func TestXxx(t *testing.T) {

	t.SkipNow()

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	var logs test.Hook
	log.Hooks.Add(&logs)
	tmpDir := t.TempDir()

	var le leaderEelector
	f := testutil.NewFakeClientFactory()

	underTest := Manager{
		K0sVars:           &config.CfgVars{ManifestsDir: tmpDir},
		KubeClientFactory: f,
		LeaderElector:     &le,
		log:               log.WithField("test", t.Name()),
	}

	require.NoError(t, underTest.Init(context.TODO()))

	le.leader = true
	for _, f := range le.acquiredCallbacks {
		f()
	}

	require.NoError(t, os.Mkdir(filepath.Join(tmpDir, "foo"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "foo", "ns.yaml"), []byte(
		`apiVersion: v1
kind: Namespace
metadata:
  name: foo`,
	), 0644))

	require.NoError(t, wait.PollUntilContextCancel(context.TODO(), 10*time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := f.Client.CoreV1().Namespaces().Get(ctx, "foo", metav1.GetOptions{})
		if err == nil {
			return true, nil
		}
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}))

	le.leader = false
	for _, f := range le.lostCallbacks {
		f()
	}

	require.NoError(t, underTest.Stop())

	le.leader = true
	for _, f := range le.acquiredCallbacks {
		f()
	}

	// t.Fail()
	// type Manager struct {
	// 	K0sVars           *config.CfgVars
	// 	KubeClientFactory kubeutil.ClientFactoryInterface

	// 	// client               kubernetes.Interface
	// 	applier    Applier
	// 	bundlePath string
	// 	cancel     atomic.Pointer[context.CancelCauseFunc]
	// 	log        *logrus.Entry

	// 	LeaderElector leaderelector.Interface
	// }

}

type leaderEelector struct {
	leader                           bool
	acquiredCallbacks, lostCallbacks []func()
}

func (l *leaderEelector) IsLeader() bool { return l.leader }

func (l *leaderEelector) AddAcquiredLeaseCallback(fn func()) {
	l.acquiredCallbacks = append(l.acquiredCallbacks, fn)
}

func (l *leaderEelector) AddLostLeaseCallback(fn func()) {
	l.lostCallbacks = append(l.lostCallbacks, fn)
}
