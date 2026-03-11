// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	"cmp"
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// Watches the directory specified by path and emits observed events to visitor.
//
// The event stream is directory-relative:
//   - [Watcher.Activated] is emitted once the watch has been established,
//   - [Watcher.Touched] is emitted for entries that appear or change,
//   - [Watcher.Gone] is emitted for entries that disappear.
//
// The function runs until ctx is done or watching fails.
//
// On Linux, watcher initialization or watch registration failures caused by
// inotify may fall back to directory polling.
func Dir(ctx context.Context, path string, visitor Visitor) error {
	return watchDir(ctx, &dirWatch{path: path, visitor: visitor})
}

// Debounces accepted watch events into "changes". A change is reported by
// invoking a callback once the directory has been quiet for a configured amount
// of time.
//
// The zero value is valid, but usually callers will set at least Delay.
type OnDirChange struct {
	// The quiet period required after the last accepted event before firing a
	// subsequent change.
	Delay time.Duration

	// Like Delay, but used for the first change. If zero, the first accepted
	// event uses Delay as well.
	InitialDelay time.Duration

	// Decides which events participate in the debounce logic and reset the
	// quiet period. If not set, all events are accepted.
	Accepts Predicate
}

// Watches path for events and invokes onChange for debounced changes.
//
// Accepted events are grouped into change bursts. After the directory has been
// inactive for the configured amount of time, onChange is invoked once for each
// burst. onChange is called sequentially, it won't be invoked concurrently.
//
// Returns nil when ctx is done, an error otherwise. If onChange returns an
// error, that one is returned by Run as well.
func (opts OnDirChange) Run(ctx context.Context, path string, onChange func(context.Context) error) (err error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer func() { cancel(err) }()

	timer := time.NewTimer(0)
	defer timer.Stop()

	var (
		lastActivity atomic.Pointer[time.Time]
		watchErr     error
	)

	changed := make(chan struct{}, 1)
	go func() {
		defer close(changed)
		watchErr = Dir(ctx, path, VisitorFunc(func(e Event) {
			if opts.Accepts == nil || opts.Accepts(e) {
				now := time.Now()
				lastActivity.Store(&now)
				select {
				case changed <- struct{}{}:
				default:
				}
			}
		}))
	}()

waitForChange:
	for initial := true; ; {
		select {
		case _, ok := <-changed:
			if !ok {
				break waitForChange
			}

			for {
				delay := opts.Delay
				if initial {
					delay = opts.InitialDelay
				}
				timer.Reset(delay - time.Since(*lastActivity.Load()))

				select {
				case <-timer.C:
					select {
					case _, ok := <-changed:
						if !ok {
							break waitForChange
						}
					default:
						if err := onChange(ctx); err != nil {
							return err
						}
						initial = false
						continue waitForChange
					}

				case <-ctx.Done():
					return nil
				}
			}

		case <-ctx.Done():
			return nil
		}
	}

	select {
	case <-ctx.Done():
		return nil
	default:
		return cmp.Or(watchErr, errors.New("watch terminated unexpectedly"))
	}
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
