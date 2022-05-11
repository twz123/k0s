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

	"github.com/stretchr/testify/assert"
)

type x struct{ initCalled, startCalled, healthyCalled, stopCalled int }

func (x *x) Initialize(context.Context) (*x, error) { x.initCalled++; return x, nil }
func (x *x) Start(context.Context) (*x, error)      { x.startCalled++; return x, nil }
func (x *x) Healthy() (err error)                   { x.healthyCalled++; return }
func (x *x) Stop() (err error)                      { x.stopCalled++; return }

func TestXxx(t *testing.T) {
	inner := x{}
	underTest := IntoComponent[*x, *x, *x](&inner)

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
