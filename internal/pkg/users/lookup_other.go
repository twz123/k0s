//go:build !unix

// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package users

import (
	"context"
	"errors"
	"fmt"
	"runtime"
)

func LookupUID(context.Context, string) (int, error) {
	return 0, fmt.Errorf("%w on %s", errors.ErrUnsupported, runtime.GOOS)
}
