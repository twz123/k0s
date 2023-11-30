/*
Copyright 2021 k0s authors

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

package dir

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// Exists checks if the given path is an existing directory. It returns true if
// the directory exists. It's different from [IsDirectory] because the latter
// will return false even if there's a real error, such as permission or general
// file system access problems. Note: This function primarily checks for the
// existence of a path and whether it's a directory. It does not guarantee that
// a directory with the given path can actually be created if it doesn't exist.
func Exists(path string) (bool, error) {
	stat, err := os.Stat(path)
	switch {
	case err == nil:
		return stat.IsDir(), nil

	case errors.Is(err, os.ErrNotExist):
		if path == "" {
			// Golang's os.Stat doesn't specify AT_EMPTY_PATH in the stat syscall.
			return false, fmt.Errorf("%w (try a dot instead of an empty path)", err)
		}

		// The path doesn't exist.
		return false, nil

	case errors.Is(err, syscall.ENOTDIR):
		// Some prefix of the path exists, but is not a directory.
		// Anyhow, the path itself doesn't exist.
		return false, nil

	default:
		return false, err
	}
}

// IsDirectory check the given path exists and is a directory
func IsDirectory(name string) bool {
	fi, err := os.Stat(name)
	return err == nil && fi.Mode().IsDir()
}

// GetAll return a list of dirs in given base path
func GetAll(base string) ([]string, error) {
	var dirs []string
	if !IsDirectory(base) {
		return dirs, fmt.Errorf("%s is not a directory", base)
	}
	fileInfos, err := os.ReadDir(base)
	if err != nil {
		return dirs, err
	}

	for _, f := range fileInfos {
		if f.IsDir() {
			dirs = append(dirs, f.Name())
		}
	}
	return dirs, nil
}

// Init creates a path if it does not exist, and verifies its permissions, if it does
func Init(path string, perm os.FileMode) error {
	if path == "" {
		return errors.New("init dir: path cannot be empty")
	}
	// if directory doesn't exist, this will create it
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}

// PathListJoin uses the OS path list separator to join a list of strings for things like PATH=x:y:z
func PathListJoin(elem ...string) string {
	return strings.Join(elem, string(os.PathListSeparator))
}
