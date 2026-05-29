//go:build !linux && !windows

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package os

import (
	"errors"
	"fmt"
	"runtime"
)

func renameNoReplace(oldPath, newPath string) error {
	return fmt.Errorf("%w on %s", errors.ErrUnsupported, runtime.GOOS)
}
