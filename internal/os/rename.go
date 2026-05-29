// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package os

import (
	_ "errors" // for godoc
	"os"
)

// Renames oldPath to newPath if and only if newPath does not already exist. If
// newPath exists, an error is returned that is [os.ErrExist] according to
// [errors.Is].
//
// The rename is atomic with respect to concurrent filesystem operations:
// Observers will not see newPath partially created, nor will the existence
// check and rename occur as separate steps.
//
// Both paths must reside on the same file system (network file systems may
// weaken guarantees about atomicity).
func RenameNoReplace(oldPath, newPath string) error {
	if err := renameNoReplace(oldPath, newPath); err != nil {
		return &os.LinkError{Op: "renameNoReplace", Old: oldPath, New: newPath, Err: err}
	}

	return nil
}
