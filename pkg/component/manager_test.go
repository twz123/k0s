/*
Copyright 2022 k0s authors

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
package component

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type Fake struct {
	InitErr      error
	RunFunc      func(context.Context) error
	StopErr      error
	ReconcileErr error

	InitCalled bool
	RunCalled  bool
	StopCalled bool
}

func (f *Fake) Init(_ context.Context) error {
	f.InitCalled = true
	return f.InitErr
}

func (f *Fake) Run(ctx context.Context) error {
	f.RunCalled = true
	if f.RunFunc != nil {
		return f.RunFunc(ctx)
	}

	return nil
}

func (f *Fake) Stop() error {
	f.StopCalled = true
	return f.StopErr
}

func TestManagerSuccess(t *testing.T) {
	m := NewManager()
	require.NotNil(t, m)

	ctx := context.Background()
	f1 := &Fake{}
	m.Add(ctx, f1)

	f2 := &Fake{}
	m.Add(ctx, f2)

	require.NoError(t, m.Init(ctx))
	require.True(t, f1.InitCalled)
	require.True(t, f2.InitCalled)

	require.NoError(t, m.Start(ctx))
	require.True(t, f1.RunCalled)
	require.True(t, f2.RunCalled)

	require.NoError(t, m.Stop())
	require.True(t, f1.StopCalled)
	require.True(t, f2.StopCalled)
}

func TestManagerInitFail(t *testing.T) {
	m := NewManager()
	require.NotNil(t, m)

	ctx := context.Background()
	f1 := &Fake{
		InitErr: fmt.Errorf("failed"),
	}
	m.Add(ctx, f1)

	f2 := &Fake{}
	m.Add(ctx, f2)

	require.Error(t, m.Init(ctx))

	// all init should be called even if any fails
	require.True(t, f1.InitCalled)
	require.True(t, f2.InitCalled)
}

func TestManagerRunFail(t *testing.T) {
	m := NewManager()
	require.NotNil(t, m)

	ctx := context.Background()

	f1 := &Fake{}
	m.Add(ctx, f1)

	f2 := &Fake{
		RunFunc: func(ctx context.Context) error { return assert.AnError },
	}
	m.Add(ctx, f2)

	f3 := &Fake{}
	m.Add(ctx, f3)

	require.Error(t, m.Start(ctx))
	require.True(t, f1.RunCalled)
	require.True(t, f2.RunCalled)
	require.False(t, f3.RunCalled)

	require.True(t, f1.StopCalled)
	require.False(t, f2.StopCalled)
	require.False(t, f3.StopCalled)
}

func TestManagerStartTimeout(t *testing.T) {
	m := NewManager()
	m.HealthyTimeout = 300 * time.Millisecond
	require.NotNil(t, m)

	ctx := context.TODO()

	f := &Fake{
		RunFunc: func(ctx context.Context) error { <-ctx.Done(); return nil },
	}
	m.Add(ctx, f)

	err := m.Start(ctx)
	if assert.Error(t, err) {
		assert.Equal(t, "Fake didn't start in time", err.Error())
	}
	assert.True(t, f.RunCalled)
	assert.True(t, f.StopCalled)
}

func TestManagerStartTimeoutErr(t *testing.T) {
	m := NewManager()
	m.HealthyTimeout = 300 * time.Millisecond
	require.NotNil(t, m)

	ctx := context.TODO()

	f := &Fake{
		RunFunc: func(ctx context.Context) error { <-ctx.Done(); return errors.New("Fake.Run() failed") },
	}
	m.Add(ctx, f)

	err := m.Start(ctx)
	if assert.Error(t, err) {
		assert.Equal(t, "Fake didn't start in time; Fake.Run() failed", err.Error())
	}
	assert.True(t, f.RunCalled)
	assert.False(t, f.StopCalled)
}
