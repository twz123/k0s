package leaderelector

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/sync/value"
	"github.com/k0sproject/k0s/pkg/leaderelection"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLeasePoolYieldSuspendsAndResumes(t *testing.T) {
	client := fakeLeaseClient{t, make(chan context.Context, 1)}
	underTest := &LeasePool{
		log:    newTestLogger(),
		client: &client,
	}

	require.NoError(t, underTest.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, underTest.Stop()) })

	firstRunCtx := client.nextRun()

	select {
	case <-firstRunCtx.Done():
		require.Fail(t, "Lease run finished unexpectedly early: %v", context.Cause(firstRunCtx))
	default:
	}

	underTest.suspendFor(20 * time.Millisecond)

	select {
	case <-firstRunCtx.Done():
		assert.ErrorContains(t, context.Cause(firstRunCtx), "lease pool is yielding")
	case <-time.After(time.Second):
		require.Fail(t, "Expected initial lease run to be canceled after suspend")
	}

	start := time.Now()
	secondRunCtx := client.nextRun()
	require.NotNil(t, secondRunCtx)
	require.NotSame(t, firstRunCtx, secondRunCtx)
	require.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)

	require.NoError(t, underTest.Stop())
	require.ErrorContains(t, context.Cause(secondRunCtx), "lease pool is stopping")
}

func TestLeasePoolMultipleYieldsResetDelay(t *testing.T) {
	client := fakeLeaseClient{t, make(chan context.Context, 1)}
	underTest := &LeasePool{
		log:          newTestLogger(),
		invocationID: "test-invocation",
		name:         "test-lease",
		client:       &client,
		status:       value.Latest[leaderelection.Status]{},
	}

	require.NoError(t, underTest.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, underTest.Stop()) })

	firstRunCtx := client.nextRun()

	underTest.suspendFor(1 * time.Hour)
	time.Sleep(15 * time.Millisecond)
	start := time.Now()
	underTest.suspendFor(40 * time.Millisecond)

	select {
	case <-firstRunCtx.Done():
		assert.ErrorContains(t, context.Cause(firstRunCtx), "lease pool is yielding")
	case <-time.After(time.Second):
		require.Fail(t, "expected initial lease run to be canceled")
	}

	secondRunCtx := client.nextRun()
	require.NotNil(t, secondRunCtx)
	require.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond)
	require.NoError(t, underTest.Stop())
	require.ErrorContains(t, context.Cause(secondRunCtx), "lease pool is stopping")
}

func newTestLogger() *logrus.Entry {
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	return logrus.NewEntry(logger)
}

type fakeLeaseClient struct {
	t     *testing.T
	runCh chan context.Context
}

func (f *fakeLeaseClient) Run(ctx context.Context, changed func(leaderelection.Status)) {
	select {
	case f.runCh <- ctx:
	default:
		assert.Fail(f.t, "channel is full")
	}

	changed(leaderelection.StatusLeading)
	<-ctx.Done()
	changed(leaderelection.StatusPending)
}

func (f *fakeLeaseClient) nextRun() context.Context {
	f.t.Helper()
	select {
	case ctx := <-f.runCh:
		return ctx
	case <-time.After(2 * time.Second):
		require.Fail(f.t, "timed out waiting for lease client run")
		return nil
	}
}
