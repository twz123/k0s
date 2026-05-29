// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package os

import (
	"os"

	"golang.org/x/sys/windows"
)

func renameNoReplace(oldPath, newPath string) error {
	oldp, err := windows.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}

	newp, err := windows.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}

	// https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw
	err = windows.MoveFileEx(oldp, newp, windows.MOVEFILE_WRITE_THROUGH)
	return os.NewSyscallError("MoveFileEx", err)
}
