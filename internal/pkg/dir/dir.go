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
	"path/filepath"
	"strings"
)

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

// Checks if basePath is a parent of childPath. Note that this function does
// only lexical processing on the paths. As a consequence, it will only work if
// the paths are given in their canonical form. This is especially important for
// case-insensitive file systems.
func IsParent(basePath, childPath string) bool {
	basePath = filepath.Clean(basePath)
	childPath = filepath.Clean(childPath)

	for                                                    // Iterate through the parent directories of childPath
	current, parent := childPath, filepath.Dir(childPath); // Start with childPath and its parent
	parent != current;                                     // Stop when there's no further parent directory
	current, parent = parent, filepath.Dir(parent) {       // Proceed to the parent of the parent
		// Check if the parent directory matches basePath.
		if parent == basePath {
			return true // The parent is the basePath!
		}
	}

	return false // Reached the top-most parent of childPath without seeing basePath.
}
