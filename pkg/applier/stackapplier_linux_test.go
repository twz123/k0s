// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package applier

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/k0sproject/k0s/pkg/k0scontext"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

func TestStackApplierRun_FallsBackToPollingWhenHittingNOFILELimits(t *testing.T) {
	// This tests the polling fallback of the stack applier when an inotify
	// watch cannot be created due to resource limits. This may occur if the
	// number of open inotify watches per user exceeds the
	// fs.inotify.max_user_instances sysctl setting or if the maximum number of
	// open files per process has been reached. These two conditions share the
	// same error code and cannot be distinguished by the caller without further
	// investigation. It is not easy to tune a sysctl just for a unit test.
	// However, we _can_ set the process-wide file descriptor limits. The
	// downside is that polling won't work either. So wait until the applier
	// goes into poll mode. Then, reset the file descriptor limit to its
	// original value. Finally, wait for the applier to complete successfully.

	stackPath := t.TempDir()
	for i := range 3 {
		ns := corev1.Namespace{
			TypeMeta:   v1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "Namespace"},
			ObjectMeta: v1.ObjectMeta{Name: "ns" + strconv.Itoa(i)},
		}
		bytes, err := yaml.Marshal(&ns)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(stackPath, fmt.Sprintf("dummy_%d.yaml", i)), bytes, 0644))
	}

	var original syscall.Rlimit
	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_NOFILE, &original))
	resetNOFILE := sync.OnceFunc(func() {
		assert.NoError(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &original), "failed to reset RLIMIT_NOFILE")
	})

	adjusted := original
	adjusted.Cur = 0
	t.Logf("Setting RLIMIT_NOFILE from %+v to %+v", original, adjusted)
	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_NOFILE, &adjusted), "failed to set RLIMIT_NOFILE")
	t.Cleanup(resetNOFILE)

	clientFactory := testutil.NewFakeClientFactory()
	log, hook := test.NewNullLogger()
	underTest := NewStackApplier(stackPath, clientFactory)
	underTest.log = log

	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)

	ctx := k0scontext.WithValue(t.Context(), &stackApplierPollOptions{
		force:    true,
		interval: 100 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	wg.Go(func() { assert.NoError(t, underTest.Run(ctx)) })
	wg.Go(func() {
		defer cancel()
		namespaces := clientFactory.Client.CoreV1().Namespaces()
		seen := make(map[string]struct{})
		assert.NoError(t, watch.FromClient[*corev1.NamespaceList, corev1.Namespace](namespaces).
			Until(t.Context(), func(item *corev1.Namespace) (done bool, err error) {
				seen[item.Name] = struct{}{}
				if len(seen) >= 3 {
					return true, nil
				}
				return false, nil
			}))
	})

	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		entries := hook.AllEntries()
		if assert.NotEmpty(t, entries) {
			assert.Equal(t, "Falling back to polling", entries[0].Message)
			if err, ok := entries[0].Data["error"].(error); assert.True(t, ok) {
				assert.ErrorIs(t, err, syscall.EMFILE)
				assert.Equal(t, "failed to create watcher: per-process limit on the number of open file descriptors has been reached", err.Error())
			}
		}
	}, 5*time.Second, 350*time.Millisecond)

	resetNOFILE()

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		assert.Fail(t, "Stack applier is still running")
	}
}
