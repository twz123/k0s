// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package leaderelection

import (
	"context"
	"errors"
)

// Indicates that the previously gained lead has been lost.
var ErrLostLead = errors.New("lost the lead")

// Returns the current leader election status. Whenever the status becomes
// outdated, the returned expired channel will be closed.
type StatusFunc func() (current Status, expired <-chan struct{})

// Runs the provided tasks function when the lead is taken. It continuously
// monitors the leader election status using statusFunc. When the lead is taken,
// the tasks function is called with a context that is canceled either when the
// lead has been lost or ctx is done. After the tasks function returns, the
// process is repeated until ctx is done.
func RunLeaderTasks(ctx context.Context, statusFunc StatusFunc, tasks func(context.Context)) {
	runStatusTasks(ctx, statusFunc, StatusLeading, ErrLostLead, tasks)
}

// Runs the provided tasks function when the lead is pending. It continuously
// monitors the leader election status using statusFunc. When the lead is
// pending, the tasks function is called with a context that is canceled either
// when the lead has been taken or ctx is done. After the tasks function
// returns, the process is repeated until ctx is done.
func RunPendingTasks(ctx context.Context, statusFunc StatusFunc, tasks func(context.Context)) {
	runStatusTasks(ctx, statusFunc, StatusPending, errors.New("taken the lead"), tasks)
}

// Runs the provided tasks function when observing the desired status. It
// continuously monitors the leader election status using statusFunc. When the
// desired status is observed, the tasks function is called with a context that
// is canceled either when the desired status is no longer observed or ctx is
// done. After the tasks function returns, the process is repeated until ctx is
// done.
func runStatusTasks(ctx context.Context, statusFunc StatusFunc, desiredStatus Status, statusChangedErr error, tasks func(context.Context)) {
	for {
		status, statusExpired := statusFunc()

		if status == desiredStatus {
			ctx, cancel := context.WithCancelCause(ctx)
			go func() {
				select {
				case <-statusExpired:
					cancel(statusChangedErr)
				case <-ctx.Done():
				}
			}()

			tasks(ctx)
		}

		select {
		case <-statusExpired:
		case <-ctx.Done():
			return
		}
	}
}
