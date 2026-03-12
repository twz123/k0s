// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	"io/fs"
)

// Receives watch events.
//
// Event names are relative to the watched directory. Callers that need the
// full path must join the watched directory path with the reported name.
type Watcher interface {
	// Reports that watching has started for a path. The path reported will be
	// the path that has been used to start the watch.
	Activated(path string)

	// Reports that an entry has been created or changed in any way other than
	// disappearing.
	//
	// The lazy [fs.FileInfo] accessor may be used to stat the entry, avoiding
	// the need for the caller to perform path manipulations. Depending on the
	// backing implementation, the results may or may not be cached, enabling
	// implementations to avoid an extra stat when the metadata is already
	// known.
	Touched(name string, info func() (fs.FileInfo, error))

	// Reports that an entry that previously existed has disappeared.
	Gone(name string)
}

// An opaque watch event that can dispatch itself to a [Watcher].
type Event interface {
	Accept(Watcher)
}

// Decides whether an [Event] is relevant to a consumer or not.
type Predicate func(Event) bool

// Returns a predicate that rejects [Watcher.Activated] events and accepts all
// other events.
//
// This is useful when a caller wants to react only to subsequent events and is
// not interested in the initial watcher activation.
func RejectActivations() Predicate {
	return func(e Event) bool {
		var denied bool
		funcs := WatcherFuncs{
			OnActivated: func(string) { denied = true },
		}
		e.Accept(&funcs)
		return !denied
	}
}

// Returns a predicate that rejects [Watcher.Touched] and [Watcher.Gone] events
// for names for which deny returns true.
//
// All other events are always accepted.
func RejectNames(deny func(string) bool) Predicate {
	return func(e Event) bool {
		var denied bool
		funcs := WatcherFuncs{
			OnTouched: func(name string, _ func() (fs.FileInfo, error)) { denied = deny(name) },
			OnGone:    func(name string) { denied = deny(name) },
		}
		e.Accept(&funcs)
		return !denied
	}
}

// Consumes [Event] values.
type Visitor interface {
	Visit(e Event)
}

// Adapts a function to the [Visitor] interface.
type VisitorFunc func(e Event)

func (f VisitorFunc) Visit(e Event) { f(e) }

// Adapts individual callbacks to the [Watcher] interface.
//
// Any nil callback is ignored. This is mainly useful for simple one-off
// consumers, e.g. for implementing predicates in terms of watcher methods
// without defining a dedicated type.
type WatcherFuncs struct {
	OnActivated func(path string)
	OnTouched   func(name string, info func() (fs.FileInfo, error))
	OnGone      func(name string)
}

// Visit implements [Visitor].
func (f *WatcherFuncs) Visit(e Event) {
	e.Accept(f)
}

// Activated implements [Watcher].
func (f *WatcherFuncs) Activated(path string) {
	if f.OnActivated != nil {
		f.OnActivated(path)
	}
}

// Touched implements [Watcher].
func (f *WatcherFuncs) Touched(name string, info func() (fs.FileInfo, error)) {
	if f.OnTouched != nil {
		f.OnTouched(name, info)
	}
}

// Gone implements [Watcher].
func (f *WatcherFuncs) Gone(name string) {
	if f.OnGone != nil {
		f.OnGone(name)
	}
}
