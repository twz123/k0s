//go:build linux

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

type polledDirEntry struct {
	name    string
	size    int64
	modTime time.Time
}

func (d *dirWatch) runPolling(ctx context.Context, log logrus.FieldLogger, pollInterval time.Duration) error {
	if ctx.Err() != nil {
		return nil // The context is already done.
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	polled, err := pollDirEntries(d.path, nil, nil)
	if err != nil {
		return fmt.Errorf("initial poll failed: %w", err)
	}

	d.fire(func(watcher Watcher) {
		watcher.Activated(d.path)
	})

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			log.Debug("Polling for changes")
			var err error
			polled, err = pollDirEntries(d.path, polled, d.fire)
			if err != nil {
				return fmt.Errorf("failed to poll: %w", err)
			}
			timer.Reset(wait.Jitter(pollInterval, 0.2))
		}
	}
}

func pollDirEntries(path string, prev []polledDirEntry, fire func(func(Watcher))) ([]polledDirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	current := make([]polledDirEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}

		current = append(current, polledDirEntry{
			name:    entry.Name(),
			size:    info.Size(),
			modTime: info.ModTime(),
		})

		var (
			unchanged bool
			i         uint
		)
		for prevLen := uint(len(prev)); i < prevLen; i++ {
			name := prev[i].name
			if cmp := strings.Compare(name, entry.Name()); cmp < 0 {
				if fire != nil {
					fire(func(w Watcher) {
						w.Gone(name)
					})
				}
			} else if cmp == 0 {
				unchanged = prev[i].size == info.Size() && prev[i].modTime.Equal(info.ModTime())
				i++
				break
			} else {
				break
			}
		}
		prev = prev[i:]

		if !unchanged && fire != nil {
			name := entry.Name()
			fire(func(w Watcher) {
				w.Touched(name, func() (fs.FileInfo, error) { return info, nil })
			})
		}
	}

	if fire != nil {
		for i := range prev {
			name := prev[i].name
			fire(func(w Watcher) {
				w.Gone(name)
			})
		}
	}

	return current, nil
}
