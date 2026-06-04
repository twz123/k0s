// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package os

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"

	osunix "github.com/k0sproject/k0s/internal/os/unix"

	"golang.org/x/sys/unix"
)

func renameNoReplace(oldPath, newPath string) (err error) {
	newDirPath, newName := filepath.Split(newPath)
	if newDirPath == "" {
		newDirPath = "."
	}

	oldDirPath, oldName := filepath.Split(oldPath)
	if oldDirPath == "" {
		oldDirPath = "."
	}

	oldDir, err := osunix.OpenDir(oldDirPath, 0)
	if err != nil {
		return err
	}
	defer func() {
		if err == nil {
			err = oldDir.Sync()
		}
		err = errors.Join(err, oldDir.Close())
	}()

	var newDir *osunix.Dir
	if oldDirPath != newDirPath {
		newDir, err = osunix.OpenDir(newDirPath, 0)
		if err != nil {
			return err
		}
		defer func() {
			if err == nil {
				err = newDir.Sync()
			}
			err = errors.Join(err, newDir.Close())
		}()
	}

	// https: //www.man7.org/linux/man-pages/man2/renameat2.2.html
	return os.NewSyscallError("renameat2", unix.Renameat2(
		// Both oldDir and newDir won't be gc'd in-flight due to the defer calls.
		// So it's safe to use the fds directly.
		int(oldDir.Fd()),
		oldName,
		int(cmp.Or(newDir, oldDir).Fd()),
		newName,
		unix.RENAME_NOREPLACE,
	))
}
