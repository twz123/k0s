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
	"os"
	"path/filepath"
	"strings"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"go.uber.org/multierr"
)

type Sink interface {
	Save(fileName string, content []byte) error
}

type fsDirSink struct {
	rootDir           string
	dirPerm, filePerm os.FileMode
	allowOverwrites   bool
}

// NewFsDirSink creates and initializes a new Sink that stores the specified
// contents in a file with the specified file name under its root directory.
// Saving fails if the file already exists or if the filename is ill-formed.
func NewFsDirSink(rootDir string, dirPerm, filePerm os.FileMode) (Sink, error) {
	absDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain absolute path for %q: %w", rootDir, err)
	}

	err = dir.Init(absDir, dirPerm)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize root directory %q: %w", rootDir, err)
	}

	return &fsDirSink{absDir, dirPerm, filePerm, false}, nil
}

func (f *fsDirSink) Save(fileName string, content []byte) error {
	absPath, err := f.toAbsolutePath(fileName)
	if err != nil {
		return err
	}

	err = dir.Init(filepath.Dir(absPath), f.dirPerm)
	if err != nil {
		return fmt.Errorf("failed to initialize directory for %q: %w", fileName, err)
	}

	file, err := os.OpenFile(absPath, f.writeFlags(), f.filePerm)
	if err == nil {
		_, err = file.Write(content)
		err = multierr.Append(err, file.Close())
	}
	if err != nil {
		return fmt.Errorf("failed to create %q: %w", fileName, err)
	}

	return nil
}

func (f *fsDirSink) toAbsolutePath(fileName string) (string, error) {
	absFileName := filepath.Join(f.rootDir, fileName)
	rel, err := filepath.Rel(f.rootDir, absFileName)
	if err != nil {
		return "", fmt.Errorf("failed to compute absolute file name for %q: %w", fileName, err)
	}

	if rel != fileName || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("not a relative file name: %q", fileName)
	}

	return absFileName, nil
}

func (f *fsDirSink) writeFlags() int {
	const flags = os.O_WRONLY | os.O_CREATE
	if f.allowOverwrites {
		return flags | os.O_TRUNC
	} else {
		return flags | os.O_EXCL
	}
}
