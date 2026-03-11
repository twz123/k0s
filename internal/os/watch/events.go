// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	"io/fs"
)

type Watcher interface {
	Activated(path string)
	Touched(name string, info func() (fs.FileInfo, error))
	Removed(name string)
}

type Event interface {
	Accept(Watcher)
}

type EventPredicate func(Event) bool

func DenyActivations() EventPredicate {
	return func(e Event) bool {
		var denied bool
		funcs := WatcherFuncs{
			OnActivated: func(string) { denied = true },
		}
		e.Accept(&funcs)
		return !denied
	}
}

func DenyNames(deny func(string) bool) EventPredicate {
	return func(e Event) bool {
		var denied bool
		funcs := WatcherFuncs{
			OnTouched: func(name string, _ func() (fs.FileInfo, error)) { denied = deny(name) },
			OnRemoved: func(name string) { denied = deny(name) },
		}
		e.Accept(&funcs)
		return !denied
	}
}

type Visitor interface {
	Visit(e Event)
}

type VisitorFunc func(e Event)

func (f VisitorFunc) Visit(e Event) { f(e) }

type WatcherFuncs struct {
	OnActivated func(path string)
	OnRemoved   func(name string)
	OnTouched   func(name string, info func() (fs.FileInfo, error))
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

// Removed implements [Watcher].
func (f *WatcherFuncs) Removed(name string) {
	if f.OnRemoved != nil {
		f.OnRemoved(name)
	}
}
