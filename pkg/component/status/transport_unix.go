//go:build unix

// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package status

import (
	"context"
	"net"
	"os"
)

func newStatusListener(ctx context.Context, socketPath string) (net.Listener, error) {
	removeLeftovers(ctx, socketPath)
	return (&net.ListenConfig{}).Listen(ctx, "unix", socketPath)
}

func cleanupStatusListener(socketPath string) {
	_ = os.Remove(socketPath)
}

func removeLeftovers(ctx context.Context, socket string) {
	_, err := dialSocket(ctx, socket)
	if err != nil {
		_ = os.Remove(socket)
	}
}

func dialSocket(ctx context.Context, socketPath string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socketPath)
}
