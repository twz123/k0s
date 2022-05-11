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
	Reconcilable
}

type Reconcilable interface {
	// Reconcile aligns the actual state of this component with the desired cluster
	// configuration. Reconcile may only be called after Init and before Stop.
	Reconcile(context.Context, *v1beta1.ClusterConfig) error
}

type Stoppable interface {
	Stop() error
}

type Created[I Initialized[S], S Started] interface {
	Stoppable
	Initialize(context.Context) (I, error)
}

type ReconcilableCreated = Created[ReconcilableInitialized, ReconcilableStarted]

type Initialized[S Started] interface {
	Stoppable
	Start(context.Context) (S, error)
}

type ReconcilableInitialized interface {
	Initialized[ReconcilableStarted]
	Reconcilable
}

type Started interface {
	Stoppable
	Healthy() error
}

type ReconcilableStarted interface {
	Started
	Reconcilable
}

var ErrNotYetInitialized = errors.New("not yet initialized")
var ErrAlreadyInitialized = errors.New("already initialized")
var ErrNotYetRunning = errors.New("not yet running")
var ErrAlreadyRunning = errors.New("already running")
var ErrNotYetStarted = errors.New("not yet started")
var ErrAlreadyStarted = errors.New("already started")
var ErrAlreadyStopped = errors.New("already stopped")

func IntoComponent(created Created[Initialized[Started], Started]) Component {
	return &legacyComponent[Created[Initialized[Started], Started], Initialized[Started], Started]{inner: created}
}

func IntoReconcilerComponent(created ReconcilableCreated) ReconcilerComponent {
	return &recoLegacyComponent{
		legacyComponent[ReconcilableCreated, ReconcilableInitialized, ReconcilableStarted]{inner: created},
	}
}

type legacyComponent[C Created[I, S], I Initialized[S], S Started] struct {
	mu    sync.Mutex
	inner any
}

type recoLegacyComponent struct {
	legacyComponent[ReconcilableCreated, ReconcilableInitialized, ReconcilableStarted]
}

func (c *legacyComponent[C, I, S]) Init(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch inner := c.inner.(type) {
	case C:
		initialized, err := inner.Initialize(ctx)
		if err != nil {
			return err
		}

		c.inner = initialized
		return nil

	case I:
		return ErrAlreadyInitialized

	case S:
		return ErrAlreadyStarted

	default:
		return ErrAlreadyStopped
	}
}

func (c *legacyComponent[C, I, S]) Run(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch inner := c.inner.(type) {
	case C:
		return ErrNotYetInitialized

	case I:
		started, err := inner.Start(ctx)
		if err != nil {
			return err
		}

		c.inner = started
		return nil

	case S:
		return ErrAlreadyStarted

	default:
		return ErrAlreadyStopped
	}
}

func (c *legacyComponent[C, I, S]) Healthy() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch inner := c.inner.(type) {
	case C, I:
		return ErrNotYetStarted

	case S:
		return inner.Healthy()

	default:
		return ErrAlreadyStopped
	}
}

func (c *legacyComponent[C, I, S]) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if inner, ok := c.inner.(Stoppable); ok {
		if err := inner.Stop(); err != nil {
			return err
		}
	}

	c.inner = nil
	return nil
}

func (c *recoLegacyComponent) Reconcile(ctx context.Context, cfg *v1beta1.ClusterConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch inner := c.inner.(type) {
	case ReconcilableCreated:
		return ErrNotYetInitialized

	case ReconcilableInitialized:
		return inner.Reconcile(ctx, cfg)

	case ReconcilableStarted:
		return inner.Reconcile(ctx, cfg)

	default:
		return ErrAlreadyStopped
	}
}

type ComponentSkeleton[C, I, R any] struct {
	Initialize        func(context.Context, C, *v1beta1.ClusterSpec) (I, error)
	Run               func(context.Context, I, *v1beta1.ClusterSpec) (R, error)
	Reconcile         func(context.Context, R, *v1beta1.ClusterSpec) error
	Healthy           func(R) error
	StopAfterCreation func(C) error
	StopAfterInit     func(I) error
	stopWhenRunning   func(R) error
}

func (skel ComponentSkeleton[C, I, R]) Create(state C) Component {
	if skel.Reconcile == nil {
		return &component[C, I, R]{
			inner: createdComponent[C, I, R]{
				state,
				skel.Initialize,
				skel.Run,
				skel.Healthy,
				skel.StopAfterCreation,
				skel.StopAfterInit,
				skel.stopWhenRunning,
			},
		}
	}

	return &reconcileComponent[C, I, R]{
		component[C, I, R]{
			inner: createdComponent[C, I, R]{
				state,
				skel.Initialize,
				skel.Run,
				skel.Healthy,
				skel.StopAfterCreation,
				skel.StopAfterInit,
				skel.stopWhenRunning,
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
	reconcile func(context.Context, R, *v1beta1.ClusterSpec) error
}

func (c *reconcileComponent[C, I, R]) Reconcile(ctx context.Context, spec *v1beta1.ClusterSpec) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch inner := c.inner.(type) {
	case createdComponent[C, I, R]:
		return ErrNotYetRunning

	case initializedComponent[I, R]:
		return ErrNotYetRunning

	case runningComponent[R]:
		return c.reconcile(ctx, inner.state, spec)

	default:
		return ErrAlreadyStopped
	}
}

type createdComponent[C, I, R any] struct {
	state C

	initialize        func(context.Context, C, *v1beta1.ClusterSpec) (I, error)
	run               func(context.Context, I, *v1beta1.ClusterSpec) (R, error)
	healthy           func(R) error
	stopAfterCreation func(C) error
	stopAfterInit     func(I) error
	stopWhenRunning   func(R) error
}

type initializedComponent[I, R any] struct {
	state I

	run             func(context.Context, I, *v1beta1.ClusterSpec) (R, error)
	healthy         func(R) error
	stopAfterInit   func(I) error
	stopWhenRunning func(R) error
}

type runningComponent[R any] struct {
	state R

	healthy         func(R) error
	stopWhenRunning func(R) error
}

func (c *component[C, I, R]) Init(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var spec *v1beta1.ClusterSpec
	if theSpec, ok := ctx.Value("clusterSpec").(*v1beta1.ClusterSpec); ok {
		spec = theSpec
	}

	switch inner := c.inner.(type) {
	case createdComponent[C, I, R]:
		var initialized I

		if inner.initialize != nil {
			var err error
			initialized, err = inner.initialize(ctx, inner.state, spec)
			if err != nil {
				return err
			}
		}

		c.inner = initializedComponent[I, R]{
			initialized,
			inner.run,
			inner.healthy,
			inner.stopAfterInit,
			inner.stopWhenRunning,
		}

		return nil

	case initializedComponent[I, R]:
		return ErrAlreadyInitialized

	case runningComponent[R]:
		return ErrAlreadyRunning

	default:
		return ErrAlreadyStopped
	}
}

func (c *component[C, I, R]) Run(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var spec *v1beta1.ClusterSpec
	if theSpec, ok := ctx.Value("clusterSpec").(*v1beta1.ClusterSpec); ok {
		spec = theSpec
	}

	switch inner := c.inner.(type) {
	case createdComponent[C, I, R]:
		return ErrNotYetInitialized

	case initializedComponent[I, R]:
		var running R

		if inner.run != nil {
			var err error
			running, err = inner.run(ctx, inner.state, spec)
			if err != nil {
				return err
			}
		}

		c.inner = runningComponent[R]{
			running,
			inner.healthy,
			inner.stopWhenRunning,
		}

		return nil

	case runningComponent[R]:
		return ErrAlreadyRunning

	default:
		return ErrAlreadyStopped
	}
}

func (c *component[C, I, R]) Healthy() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch state := c.inner.(type) {
	case createdComponent[C, I, R]:
		return ErrNotYetInitialized

	case initializedComponent[I, R]:
		return ErrNotYetRunning

	case runningComponent[R]:
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
	case createdComponent[C, I, R]:
		if state.stopAfterCreation != nil {
			if err := state.stopAfterCreation(state.state); err != nil {
				return err
			}
		}

		c.inner = nil
		return nil

	case initializedComponent[I, R]:
		if state.stopAfterInit != nil {
			if err := state.stopAfterInit(state.state); err != nil {
				return err
			}
		}

		c.inner = nil
		return nil

	case runningComponent[R]:
		if state.stopWhenRunning != nil {
			if err := state.stopWhenRunning(state.state); err != nil {
				return err
			}
		}

		c.inner = nil
		return nil

	default:
		return ErrAlreadyStopped
	}
}

// type Uninitialized[I any] interface {
// 	Init(context.Context) (I, error)
// }

// func InitializerFunc[I any](fn func(context.Context) (I, error)) Uninitialized[I] {
// 	tr := newTransitionFunc(fn, func() error { return ErrAlreadyInitialized })
// 	return initializerFunc[I](tr.transition)
// }

// type initializerFunc[I any] func(context.Context) (I, error)

// func (f initializerFunc[I]) Init(ctx context.Context) (I, error) {
// 	return f(ctx)
// }

// type Initialized[R any] interface {
// 	Run(context.Context) (R, error)
// }

// func RunFunc[R any](fn func(context.Context) (R, error)) Initialized[R] {
// 	tr := newTransitionFunc(fn, func() error { return ErrAlreadyRunning })
// 	return runFunc[R](tr.transition)
// }

// type runFunc[R any] func(context.Context) (R, error)

// func (f runFunc[R]) Run(ctx context.Context) (R, error) {
// 	return f(ctx)
// }

// type transitionFunc[T any] struct {
// 	fn                  atomic.Value
// 	alreadyTransitioned func() error
// }

// func newTransitionFunc[T any](fn func(context.Context) (T, error), alreadyTransitioned func() error) *transitionFunc[T] {
// 	f := new(transitionFunc[T])
// 	f.fn.Store(&fn)
// 	f.alreadyTransitioned = alreadyTransitioned
// 	return f
// }

// func (f *transitionFunc[T]) transition(ctx context.Context) (transitioned T, err error) {
// 	var consumed func(context.Context) (T, error)
// 	if fn := f.fn.Swap(&consumed).(*func(context.Context) (T, error)); fn != nil {
// 		transitioned, err = (*fn)(ctx)
// 	} else {
// 		err = f.alreadyTransitioned()
// 	}

// 	return
// }
