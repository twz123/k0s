// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	"context"
)

// Watches the directory specified by path and emits observed events to visitor.
//
// The event stream is directory-relative:
//   - [Watcher.Activated] is emitted once the watch has been established,
//   - [Watcher.Touched] is emitted for entries that appear or change,
//   - [Watcher.Gone] is emitted for entries that disappear.
//
// The function runs until ctx is done or watching fails.
func Dir(ctx context.Context, path string, visitor Visitor) error {
	return (&dirWatch{path: path, visitor: visitor}).runFSNotify(ctx)
}

type dirWatch struct {
	path    string
	visitor Visitor
}

type eventFunc func(Watcher)

func (f eventFunc) Accept(w Watcher) { f(w) }

func (d *dirWatch) fire(f func(Watcher)) {
	d.visitor.Visit(eventFunc(f))
}
