//go:build !windows

// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package supervised

import (
	"context"
)

func run(ctx context.Context, main MainFunc) error {
	// This is not doing anything special yet. Explicitly store a nil interface.
	ctx = set(ctx, nil)
	return main(ctx)
}
