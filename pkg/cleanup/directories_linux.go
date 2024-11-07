/*
Copyright 2024 k0s authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cleanup

import (
	"errors"
	"os"
	"path/filepath"

	osunix "github.com/k0sproject/k0s/internal/os/unix"
	"k8s.io/mount-utils"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type directories struct {
	Config *Config
}

// Name returns the name of the step
func (d *directories) Name() string {
	return "remove directories step"
}

func (d *directories) Run() error {
	log := logrus.StandardLogger()
	mounter := mount.New("")

	removeAllSkipMountPoints(log, mounter, d.Config.dataDir)
	removeAllSkipMountPoints(log, mounter, d.Config.runDir)
	return nil
}

func removeAllSkipMountPoints(log logrus.FieldLogger, mounter mount.Interface, dirPath string) bool {
	dir, err := osunix.OpenDir(dirPath, unix.O_NOFOLLOW) // What if dir itself is a symlink?
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true
		}
		log.WithError(err).Warn("Leaving behind ", dirPath)
		return false
	}
	defer dir.Close()

	if removeNamesSkipMountPoints(log, mounter, dir, dirPath) {
		if err := os.Remove(dirPath); err == nil || errors.Is(err, os.ErrNotExist) {
			return true
		}
		log.WithError(err).Warn("Leaving behind ", dirPath)
	}

	return false
}

func removeNamesSkipMountPoints(log logrus.FieldLogger, mounter mount.Interface, dir *osunix.DirFD, dirPath string) bool {
	var leftovers bool
	for name, err := range dir.ReadEntryNames() {
		if err != nil {
			log.WithError(err).Warnf("Leaving behind %s/%s/*", dirPath, name)
			return false
		}

		if removed, err := removeNameSkipMountPoints(log, mounter, dir, dirPath, name); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.WithError(err).Warnf("Leaving behind %s/%s", dirPath, name)
				leftovers = true
			}
		} else if !removed {
			leftovers = true
		}
	}

	return !leftovers
}

func removeNameSkipMountPoints(log logrus.FieldLogger, mounter mount.Interface, dir *osunix.DirFD, dirPath, name string) (bool, error) {
	// First try to simply unlink the name.
	// The assumption here is that mount points cannot be simply unlinked.
	if fileErr := dir.Remove(name); fileErr == nil || errors.Is(fileErr, os.ErrNotExist) {
		// That worked. Mission accomplished.
		return true, nil
	} else {
		// Try to remove an empty directory.
		if dirErr := dir.RemoveDir(name); dirErr == nil || errors.Is(fileErr, os.ErrNotExist) {
			// That worked. Mission accomplished.
			return true, nil
		} else if errors.Is(dirErr, unix.ENOTDIR) {
			// It's not a directory, return the file error.
			return false, fileErr
		}
	}

	// About to recurse into a directory. Check that it's not a mount point.
	subDirPath := filepath.Join(dirPath, name)
	if isMountPoint, err := mounter.IsMountPoint(subDirPath); err != nil {
		return false, err
	} else if isMountPoint {
		log.Info("Leaving behind mount point ", subDirPath)
		return false, nil
	}

	subDir, err := dir.OpenDir(name, unix.O_NOFOLLOW)
	if err != nil {
		return false, err
	}
	defer subDir.Close()

	if removeNamesSkipMountPoints(log, mounter, subDir, subDirPath) {
		return true, dir.RemoveDir(name)
	}

	return false, nil
}
