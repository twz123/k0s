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
	"testing"

	"github.com/stretchr/testify/assert"
)

type Fake struct {
	InitErr      error
	RunErr       error
	StopErr      error
	ReconcileErr error
	HealthyFn    func() error

	InitCalled    bool
	RunCalled     bool
	StopCalled    bool
	HealthyCalled bool
}

func (f *Fake) Init(_ context.Context) error {
	f.InitCalled = true
	return f.InitErr
}
func (f *Fake) Run(_ context.Context) error {
	f.RunCalled = true
	return f.RunErr
}

func (f *Fake) Stop() error {
	f.StopCalled = true
	return f.StopErr
}
func (f *Fake) Reconcile() error { return nil }
func (f *Fake) Healthy() error {
	f.HealthyCalled = true
	if f.HealthyFn != nil {
		return f.HealthyFn()
	}
	return nil
}

func TestManagerSuccess(t *testing.T) {
	var components ManagerBuilder

	f1 := &Fake{}
	f2 := &Fake{}

	components.Add(f1)
	components.Add(f2)

	underTest := components.Build("underTest")
	err := underTest.Init(context.Background())

	assert := assert.New(t)
	assert.NoError(err)
	assert.True(f1.InitCalled)
	assert.True(f2.InitCalled)

	err = underTest.Run(context.Background())

	assert.NoError(err)
	assert.True(f1.RunCalled)
	assert.True(f2.RunCalled)
	assert.True(f1.HealthyCalled)
	assert.True(f2.HealthyCalled)

	err = underTest.Stop()

	assert.NoError(err)
	assert.True(f1.StopCalled)
	assert.True(f2.StopCalled)
}

func TestManagerInitFail(t *testing.T) {
	var components ManagerBuilder

	f1 := &Fake{InitErr: assert.AnError}
	f2 := &Fake{InitErr: assert.AnError}

	components.Add(f1)
	components.Add(f2)

	underTest := components.Build("underTest")
	err := underTest.Init(context.Background())

	if assert.Error(t, err) {
		var componentErr *Error
		if assert.True(t, errors.As(err, &componentErr), "expected a component.Error, got %v", err) {
			assert.Condition(t, func() bool {
				// Init runs concurrently, so both components may win the race
				return componentErr.Component == f1 || componentErr.Component == f2
			}, "expected component to be either f1 or f2")
			assert.Same(t, assert.AnError, componentErr.Err)
		}
	}

	// all stops should be called when a single init fails
	assert.True(t, f1.StopCalled)
	assert.True(t, f2.StopCalled)
}

func TestManagerHealthyFail(t *testing.T) {
	var components ManagerBuilder

	f1 := &Fake{}
	f2 := &Fake{HealthyFn: func() error { return assert.AnError }}

	components.Add(f1)
	components.Add(f2)

	underTest := components.Build("underTest")
	err := underTest.Healthy()

	if assert.Error(t, err) {
		var componentErr *Error
		if assert.True(t, errors.As(err, &componentErr), "expected a component.Error, got %v", err) {
			assert.Same(t, f2, componentErr.Component)
			assert.Same(t, assert.AnError, componentErr.Err)
		}
	}

	// no stops should be called when health checks fail
	assert.False(t, f1.StopCalled)
	assert.False(t, f2.StopCalled)
}

func TestManagerRunFail(t *testing.T) {
	var components ManagerBuilder

	f1 := &Fake{}
	f2 := &Fake{RunErr: assert.AnError}
	f3 := &Fake{}

	components.Add(f1)
	components.Add(f2)
	components.Add(f3)

	underTest := components.Build("underTest")
	err := underTest.Run(context.Background())

	if assert.Error(t, err) {
		var componentErr *Error
		if assert.True(t, errors.As(err, &componentErr), "expected a component.Error") {
			assert.Same(t, f2, componentErr.Component)
			assert.Contains(t, componentErr.Err.Error(), "failed to run: ")
			assert.Same(t, assert.AnError, errors.Unwrap(componentErr.Err))
		}
	}

	assert := assert.New(t)
	assert.True(f1.RunCalled)
	assert.True(f2.RunCalled)
	assert.False(f3.RunCalled)

	assert.True(f1.HealthyCalled)
	assert.False(f2.HealthyCalled)
	assert.False(f3.HealthyCalled)

	assert.True(f1.StopCalled)
	assert.True(f2.StopCalled)
	assert.True(f3.StopCalled)
}

func TestManagerRunHealthyFail(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var components ManagerBuilder

	f1 := &Fake{}
	f2 := &Fake{
		HealthyFn: func() error {
			cancel()
			return assert.AnError
		},
	}
	f3 := &Fake{}

	components.Add(f1)
	components.Add(f2)
	components.Add(f3)

	underTest := components.Build("underTest")
	err := underTest.Run(ctx)

	if assert.Error(t, err) {
		var componentErr *Error
		if assert.True(t, errors.As(err, &componentErr), "expected a component.Error, got %v", err) {
			assert.Equal(t, f2, componentErr.Component)
			assert.Contains(t, componentErr.Err.Error(), "unhealthy: ")
			assert.Same(t, assert.AnError, errors.Unwrap(componentErr.Err))
		}
	}

	assert := assert.New(t)
	assert.True(f1.RunCalled)
	assert.True(f2.RunCalled)
	assert.False(f3.RunCalled)

	assert.True(f1.HealthyCalled)
	assert.True(f2.HealthyCalled)
	assert.False(f3.HealthyCalled)

	assert.True(f1.StopCalled)
	assert.True(f2.StopCalled)
	assert.True(f3.StopCalled)
}

func TestError(t *testing.T) {
	innerErr := errors.New("inner")

	var err error = &Error{nil, innerErr}

	t.Run("Error", func(t *testing.T) {
		assert.Equal(t, "<nil>: inner", err.Error())
	})

	t.Run("Unwrap", func(t *testing.T) {
		assert.Same(t, innerErr, errors.Unwrap(err))
	})
}
