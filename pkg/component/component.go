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
	"sync"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
)

// Component defines the lifecycle of managed components.
//
//    Created ――――――――――――――――――――――►(Stop)―――╮
//    ╰―(Init)―► Initialized ―――――――►(Stop)――╮│
//               ╰―(Run)―► Running ―►(Stop)―╮││
//                  ╭(Healthy)╯▲            ▼▼▼
//                  ╰――――――――――╯         ╭► Terminated╮
//                                       ╰――――(Stop)――╯
type Component interface {
	// Init initializes the component and prepares it for execution. This should
	// include any fallible operations that can be performed without actually
	// starting the component, such as creating files and folders or validating
	// configuration settings. Init must be the first method called in the
	// component's lifecycle. Init must be called only once.
	Init(context.Context) error

	// Run starts the component. When Run returns, the component is executing in
	// the background. Run may be called only once after Init. If the component
	// is a ReconcilerComponent, a call to Reconcile may be required before Run
	// can be called. The given context is not intended to replace a call to
	// Stop when cancelled. It's merely used to cancel the component's startup.
	Run(context.Context) error

	// Healthy performs a health check and indicates that the component is
	// running and functional. Whenever this is not the case, a non-nil error is
	// returned.
	Healthy() error

	// Stop stops this component, potentially cleaning up any temporary
	// resources attached to it. Stop itself may be called in any lifecycle
	// phase. All other lifecycle methods have to return an error after Stop
	// returns. Stop may be called more than once.
	Stop() error
}

// ReconcilerComponent defines the component interface that is reconciled based
// on changes on the global config CR object changes.
//
//    Created ――――――――――――――――――――――――►(Stop)―――――╮
//    ╰―(Init)―► Initialized ―――――――――►(Stop)――――╮│
//   ╭(Reconcile)╯▲╰―(Run)―► Running ―►(Stop)―――╮││
//   ╰――――――――――――╯╭(Reconcile)╯▲▲╰(Healthy)╮   ▼▼▼
//                 ╰――――――――――――╯╰――――――――――╯╭► Terminated╮
//                                           ╰――――(Stop)――╯
type ReconcilerComponent interface {
	Component

	// Reconcile aligns the actual state of this component with the desired cluster
	// configuration. Reconcile may only be called after Init and before Stop.
	Reconcile(context.Context, *v1beta1.ClusterConfig) error
}

var ErrNotYetInitialized = errors.New("not yet initialized")
var ErrAlreadyInitialized = errors.New("already initialized")
var ErrNotYetRunning = errors.New("not yet running")
var ErrAlreadyRunning = errors.New("already running")
var ErrAlreadyStopped = errors.New("already stopped")

type ComponentSkeleton[C, I, R any] struct {
	Initialize        func(context.Context, C, *v1beta1.ClusterConfig) (I, error)
	Run               func(context.Context, I, *v1beta1.ClusterConfig) (R, error)
	Reconcile         func(context.Context, R, *v1beta1.ClusterConfig) error
	Healthy           func(R) error
	StopAfterCreation func(C) error
	StopAfterInit     func(I) error
	StopWhenRunning   func(R) error
}

func (skel ComponentSkeleton[C, I, R]) Create(state C) Component {
	if skel.Reconcile == nil {
		return &component[C, I, R]{
			inner: componentCreated[C, I, R]{
				state,
				skel.Initialize,
				skel.Run,
				skel.Healthy,
				skel.StopAfterCreation,
				skel.StopAfterInit,
				skel.StopWhenRunning,
			},
		}
	}

	return &reconcileComponent[C, I, R]{
		component[C, I, R]{
			inner: componentCreated[C, I, R]{
				state,
				skel.Initialize,
				skel.Run,
				skel.Healthy,
				skel.StopAfterCreation,
				skel.StopAfterInit,
				skel.StopWhenRunning,
			},
		},
		skel.Reconcile,
	}
}

type component[C, I, R any] struct {
	mu    sync.Mutex
	inner any
}

type reconcileComponent[C, I, R any] struct {
	component[C, I, R]
	reconcile func(context.Context, R, *v1beta1.ClusterConfig) error
}

func (c *reconcileComponent[C, I, R]) Reconcile(ctx context.Context, cfg *v1beta1.ClusterConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch inner := c.inner.(type) {
	case componentCreated[C, I, R]:
		return ErrNotYetRunning

	case ComponentInitialized[I, R]:
		return ErrNotYetRunning

	case componentRunning[R]:
		return c.reconcile(ctx, inner.state, cfg)

	default:
		return ErrAlreadyStopped
	}
}

type componentCreated[C, I, R any] struct {
	state C

	initialize        func(context.Context, C, *v1beta1.ClusterConfig) (I, error)
	run               func(context.Context, I, *v1beta1.ClusterConfig) (R, error)
	healthy           func(R) error
	stopAfterCreation func(C) error
	stopAfterInit     func(I) error
	stopWhenRunning   func(R) error
}

type ComponentInitialized[I, R any] struct {
	state I

	run             func(context.Context, I, *v1beta1.ClusterConfig) (R, error)
	healthy         func(R) error
	stopAfterInit   func(I) error
	stopWhenRunning func(R) error
}

type componentRunning[R any] struct {
	state R

	healthy         func(R) error
	stopWhenRunning func(R) error
}

func (c *component[C, I, R]) Init(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var cfg *v1beta1.ClusterConfig
	if cfgFromCtx, ok := ctx.Value("clusterConfig").(*v1beta1.ClusterConfig); ok {
		cfg = cfgFromCtx
	}

	switch inner := c.inner.(type) {
	case componentCreated[C, I, R]:
		var initialized I

		if inner.initialize != nil {
			var err error
			initialized, err = inner.initialize(ctx, inner.state, cfg)
			if err != nil {
				return err
			}
		}

		c.inner = ComponentInitialized[I, R]{
			initialized,
			inner.run,
			inner.healthy,
			inner.stopAfterInit,
			inner.stopWhenRunning,
		}

		return nil

	case ComponentInitialized[I, R]:
		return ErrAlreadyInitialized

	case componentRunning[R]:
		return ErrAlreadyRunning

	default:
		return ErrAlreadyStopped
	}
}

func (c *component[C, I, R]) Run(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var cfg *v1beta1.ClusterConfig
	if cfgFromCtx, ok := ctx.Value("clusterConfig").(*v1beta1.ClusterConfig); ok {
		cfg = cfgFromCtx
	}

	switch inner := c.inner.(type) {
	case componentCreated[C, I, R]:
		return ErrNotYetInitialized

	case ComponentInitialized[I, R]:
		var running R

		if inner.run != nil {
			var err error
			running, err = inner.run(ctx, inner.state, cfg)
			if err != nil {
				return err
			}
		}

		c.inner = componentRunning[R]{
			running,
			inner.healthy,
			inner.stopWhenRunning,
		}

		return nil

	case componentRunning[R]:
		return ErrAlreadyRunning

	default:
		return ErrAlreadyStopped
	}
}

func (c *component[C, I, R]) Healthy() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch state := c.inner.(type) {
	case componentCreated[C, I, R], ComponentInitialized[I, R]:
		return ErrNotYetRunning

	case componentRunning[R]:
		if state.healthy != nil {
			return state.healthy(state.state)
		}
		return nil

	default:
		return ErrAlreadyStopped
	}
}

func (c *component[C, I, R]) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch state := c.inner.(type) {
	case componentCreated[C, I, R]:
		if state.stopAfterCreation != nil {
			if err := state.stopAfterCreation(state.state); err != nil {
				return err
			}
		}

		c.inner = nil
		return nil

	case ComponentInitialized[I, R]:
		if state.stopAfterInit != nil {
			if err := state.stopAfterInit(state.state); err != nil {
				return err
			}
		}

		c.inner = nil
		return nil

	case componentRunning[R]:
		if state.stopWhenRunning != nil {
			if err := state.stopWhenRunning(state.state); err != nil {
				return err
			}
		}

		c.inner = nil
		return nil

	default:
		return nil // already stopped
	}
}
