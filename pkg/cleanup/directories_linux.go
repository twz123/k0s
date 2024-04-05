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
	"path/filepath"
	"slices"
	"strings"

	"github.com/k0sproject/k0s/internal/pkg/dir"

	"k8s.io/mount-utils"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func unmountRecursively(path string) (pathIsMounted bool) {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		logrus.WithError(err).Warn("Failed to normalize ", path)
		return false
	}

	mounts, err := mount.ListProcMounts("/proc/mounts")
	if err != nil {
		logrus.WithError(err).Warn("Failed enumerate file system mount points")
		return false
	}

	var pathsToUnmount []string
	for _, v := range mounts {
		mountedPath := filepath.Clean(v.Path)
		switch {
		case dir.IsParent(path, mountedPath):
			pathsToUnmount = append(pathsToUnmount, mountedPath)
		case mountedPath == path:
			pathIsMounted = true
		}
	}

	// Reverse sort the paths, so that child paths bubble up and get unmounted first.
	slices.SortFunc(pathsToUnmount, func(l, r string) int {
		return strings.Compare(r, l) // reverse sort by swapping args
	})

	for _, pathToUnmount := range pathsToUnmount {
		logrus.Debug("Attempting to unmount ", pathToUnmount)
		// Don't dereference target if it is a symbolic link. This flag allows
		// security problems to be avoided in set-user-ID-root programs that
		// allow unprivileged users to unmount file systems.
		const flags = unix.UMOUNT_NOFOLLOW
		// https://www.man7.org/linux/man-pages/man2/umount.2.html
		if err = unix.Unmount(pathToUnmount, flags); err != nil {
			logrus.WithError(err).Warn("Failed to unmount ", pathToUnmount)
		}
	}

	return pathIsMounted
}
