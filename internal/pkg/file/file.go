/*
Copyright 2022 k0s authors

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

package file

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/renameio/v2"
	"github.com/k0sproject/k0s/internal/pkg/users"
	"go.uber.org/multierr"
)

// Exists checks if a file exists and is not a directory before we
// try using it to prevent further errors.
func Exists(fileName string) bool {
	info, err := os.Stat(fileName)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

type DottedBaseName int

const (
	// NotDotted indicates that a path is not considered to be hidden on
	// Unix-like operating systems, i.e. it doesn't start with a dot.
	NotDotted DottedBaseName = iota + 1

	// Dotted indicates that a path is considered to be hidden on Unix-like
	// operating systems, i.e. it starts with a dot.
	Dotted

	// RelativeBase indicates that it cannot be decided if the path is
	// considered to be hidden on Unix-like operating systems or not, since it's
	// base name is relative.
	RelativeBase
)

func (d DottedBaseName) String() string {
	switch d {
	case NotDotted:
		return "NotDotted"
	case Dotted:
		return "Dotted"
	case RelativeBase:
		return "RelativeBase"
	}

	return strconv.FormatInt(int64(d), 10)
}

// IsDottedBaseName checks if the given path is considered to be hidden on
// Unix-like operating systems, i.e. if its base name starts with a dot.
func IsDottedBaseName(path string) DottedBaseName {
	base := filepath.Base(filepath.Clean(path))
	if base == "." || base == ".." {
		return RelativeBase
	}
	if base[0] == '.' {
		return Dotted
	}
	return NotDotted
}

// Chown changes file/dir mode
func Chown(file, owner string, permissions os.FileMode) error {
	// Chown the file properly for the owner
	uid, _ := users.GetUID(owner)
	err := os.Chown(file, uid, -1)
	if err != nil && os.Geteuid() == 0 {
		return err
	}
	err = os.Chmod(file, permissions)
	if err != nil && os.Geteuid() == 0 {
		return err
	}
	return nil
}

// Copy copies file from src to dst
func Copy(src, dst string) error {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}

	input, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("error reading source file (%v): %v", src, err)
	}

	err = os.WriteFile(dst, input, sourceFileStat.Mode())
	if err != nil {
		return fmt.Errorf("error writing destination file (%v): %v", dst, err)
	}
	return nil
}

func WriteTmpFile(data string, prefix string) (path string, err error) {
	tmpFile, err := os.CreateTemp("", prefix)
	if err != nil {
		return "", fmt.Errorf("cannot create temporary file: %v", err)
	}

	text := []byte(data)
	if _, err = tmpFile.Write(text); err != nil {
		return "", fmt.Errorf("failed to write to temporary file: %v", err)
	}

	return tmpFile.Name(), nil
}

func WriteContentAtomically(fileName string, content []byte, perm os.FileMode) error {
	return WriteAtomically(fileName, perm, func(file io.Writer) error {
		_, err := file.Write(content)
		return err
	})
}

func WriteAtomically(fileName string, perm os.FileMode, write func(file io.Writer) error) (err error) {
	file, err := renameio.NewPendingFile(
		fileName,
		renameio.WithTempDir(filepath.Base(fileName)),
		renameio.WithPermissions(perm),
	)
	if err != nil {
		return err
	}
	defer func() {
		err = multierr.Append(err, file.Cleanup())
	}()

	if err := write(file); err != nil {
		return err
	}

	return file.CloseAtomicallyReplace()
}
