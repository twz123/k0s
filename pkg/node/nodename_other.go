//go:build !windows

package node

import "context"

func defaultNodenameOverride(ctx context.Context) (string, error) {
	return "", nil // no-op on non-windows
}
