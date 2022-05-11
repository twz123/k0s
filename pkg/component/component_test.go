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
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/stretchr/testify/assert"
)

type ic struct{ initCalled, startCalled, healthyCalled, stopCalled int }

func (x *ic) Initialize(context.Context) (Initialized[Started], error) { x.initCalled++; return x, nil }
func (x *ic) Start(context.Context) (Started, error)                   { x.startCalled++; return x, nil }
func (x *ic) Healthy() (err error)                                     { x.healthyCalled++; return }
func (x *ic) Stop() (err error)                                        { x.stopCalled++; return }

func TestIntoComponent(t *testing.T) {
	inner := ic{}
	underTest := IntoComponent(&inner)

	assert := assert.New(t)

	assert.Same(ErrNotYetInitialized, underTest.Run(context.TODO()))
	assert.Equal(0, inner.initCalled)
	assert.Equal(0, inner.startCalled)
	assert.Equal(0, inner.healthyCalled)
	assert.Equal(0, inner.stopCalled)

	assert.Same(ErrNotYetStarted, underTest.Healthy())
	assert.Equal(0, inner.initCalled)
	assert.Equal(0, inner.startCalled)
	assert.Equal(0, inner.healthyCalled)
	assert.Equal(0, inner.stopCalled)

	assert.NoError(underTest.Init(context.TODO()))
	assert.Equal(1, inner.initCalled)
	assert.Equal(0, inner.startCalled)
	assert.Equal(0, inner.healthyCalled)
	assert.Equal(0, inner.stopCalled)

	assert.Same(ErrAlreadyInitialized, underTest.Init(context.TODO()))
	assert.Equal(1, inner.initCalled)
	assert.Equal(0, inner.startCalled)
	assert.Equal(0, inner.healthyCalled)
	assert.Equal(0, inner.stopCalled)

	assert.Same(ErrNotYetStarted, underTest.Healthy())
	assert.Equal(1, inner.initCalled)
	assert.Equal(0, inner.startCalled)
	assert.Equal(0, inner.healthyCalled)
	assert.Equal(0, inner.stopCalled)

	assert.NoError(underTest.Run(context.TODO()))
	assert.Equal(1, inner.initCalled)
	assert.Equal(1, inner.startCalled)
	assert.Equal(0, inner.healthyCalled)
	assert.Equal(0, inner.stopCalled)

	assert.Same(ErrAlreadyStarted, underTest.Init(context.TODO()))
	assert.Equal(1, inner.initCalled)
	assert.Equal(1, inner.startCalled)
	assert.Equal(0, inner.healthyCalled)
	assert.Equal(0, inner.stopCalled)

	assert.NoError(underTest.Healthy())
	assert.Equal(1, inner.initCalled)
	assert.Equal(1, inner.startCalled)
	assert.Equal(1, inner.healthyCalled)
	assert.Equal(0, inner.stopCalled)

	assert.NoError(underTest.Stop())
	assert.Equal(1, inner.initCalled)
	assert.Equal(1, inner.startCalled)
	assert.Equal(1, inner.healthyCalled)
	assert.Equal(1, inner.stopCalled)

	assert.Same(ErrAlreadyStopped, underTest.Init(context.TODO()))
	assert.Equal(1, inner.initCalled)
	assert.Equal(1, inner.startCalled)
	assert.Equal(1, inner.healthyCalled)
	assert.Equal(1, inner.stopCalled)

	assert.Same(ErrAlreadyStopped, underTest.Run(context.TODO()))
	assert.Equal(1, inner.initCalled)
	assert.Equal(1, inner.startCalled)
	assert.Equal(1, inner.healthyCalled)
	assert.Equal(1, inner.stopCalled)

	assert.Same(ErrAlreadyStopped, underTest.Healthy())
	assert.Equal(1, inner.initCalled)
	assert.Equal(1, inner.startCalled)
	assert.Equal(1, inner.healthyCalled)
	assert.Equal(1, inner.stopCalled)

	assert.NoError(underTest.Stop())
	assert.Equal(1, inner.initCalled)
	assert.Equal(1, inner.startCalled)
	assert.Equal(1, inner.healthyCalled)
	assert.Equal(1, inner.stopCalled)
}

func TestSkel(t *testing.T) {
	assert := assert.New(t)

	type state struct{ called, healthyCalled, stopCalled int }
	createdState := state{}
	initState := state{}
	runState := state{}

	skel := ComponentSkeleton[*state, *state, *state]{
		Initialize: func(ctx context.Context, s *state, cc *v1beta1.ClusterConfig) (*state, error) {
			assert.Same(&createdState, s)
			initState.called++
			return &initState, nil
		},
		Run: func(ctx context.Context, s *state, cc *v1beta1.ClusterConfig) (*state, error) {
			assert.Same(&initState, s)
			runState.called++
			return &runState, nil
		},
		Healthy: func(s *state) error {
			assert.Same(&runState, s)
			s.healthyCalled++
			return nil
		},
		StopAfterCreation: func(s *state) error {
			assert.Same(&createdState, s)
			s.stopCalled++
			return nil
		},
		StopAfterInit: func(s *state) error {
			assert.Same(&initState, s)
			s.stopCalled++
			return nil
		},
		StopWhenRunning: func(s *state) error {
			assert.Same(&runState, s)
			s.stopCalled++
			return nil
		},
	}

	underTest := skel.Create(&createdState)

	assert.Same(ErrNotYetInitialized, underTest.Run(context.TODO()))
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{0, 0, 0}, initState)
	assert.Equal(state{0, 0, 0}, runState)

	assert.Same(ErrNotYetRunning, underTest.Healthy())
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{0, 0, 0}, initState)
	assert.Equal(state{0, 0, 0}, runState)

	assert.NoError(underTest.Init(context.TODO()))
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{0, 0, 0}, runState)

	assert.Same(ErrAlreadyInitialized, underTest.Init(context.TODO()))
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{0, 0, 0}, runState)

	assert.Same(ErrNotYetRunning, underTest.Healthy())
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{0, 0, 0}, runState)

	assert.NoError(underTest.Run(context.TODO()))
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{1, 0, 0}, runState)

	assert.Same(ErrAlreadyRunning, underTest.Init(context.TODO()))
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{1, 0, 0}, runState)

	assert.NoError(underTest.Healthy())
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{1, 1, 0}, runState)

	assert.NoError(underTest.Stop())
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{1, 1, 1}, runState)

	assert.Same(ErrAlreadyStopped, underTest.Init(context.TODO()))
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{1, 1, 1}, runState)

	assert.Same(ErrAlreadyStopped, underTest.Run(context.TODO()))
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{1, 1, 1}, runState)

	assert.Same(ErrAlreadyStopped, underTest.Healthy())
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{1, 1, 1}, runState)

	assert.NoError(underTest.Stop())
	assert.Equal(state{0, 0, 0}, createdState)
	assert.Equal(state{1, 0, 0}, initState)
	assert.Equal(state{1, 1, 1}, runState)
}
