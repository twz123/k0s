/*
Copyright 2020 k0s authors

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

package assets

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/constant"

	"github.com/sirupsen/logrus"
)

// EmbeddedBinaryNeedsUpdate returns true if the provided embedded binary file should
// be updated. This determination is based on the modification times and file sizes of both
// the provided executable and the embedded executable. It is expected that the embedded binary
// modification times should match the main `k0s` executable.
func EmbeddedBinaryNeedsUpdate(exinfo os.FileInfo, embeddedBinaryPath string, size int64) bool {
	if pathinfo, err := os.Stat(embeddedBinaryPath); err == nil {
		return !exinfo.ModTime().Equal(pathinfo.ModTime()) || pathinfo.Size() != size
	}

	// If the stat fails, the file is either missing or permissions are missing
	// to read this -- let above know that an update should be attempted.

	return true
}

// BinPath searches for a binary on disk:
// - in the BinDir folder,
// - in the PATH.
// The first to be found is the one returned.
func BinPath(name string, binDir string) string {
	executable := name + constant.ExecutableSuffix
	// Look into the BinDir folder.
	path := filepath.Join(binDir, executable)
	if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
		return path
	}

	// If we still haven't found the executable, look for it in the PATH.
	if path, err := exec.LookPath(executable); err == nil {
		path, _ := filepath.Abs(path)
		return path
	}
	return executable
}

func StageExecutable(dataDir string, name string) error {
	return Stage(dataDir, name+constant.ExecutableSuffix, 0755)
}

// Stage ...
func Stage(dataDir string, name string, perm os.FileMode) error {
	p := filepath.Join(dataDir, name)
	logrus.Infof("Staging '%s'", p)

	selfexe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("unable to determine current executable: %w", err)
	}

	exinfo, err := os.Stat(selfexe)
	if err != nil {
		return fmt.Errorf("unable to stat '%s': %w", selfexe, err)
	}

	gzname := "bin/" + name + ".gz"
	bin, embedded := BinData[gzname]
	if !embedded {
		logrus.Debug("Skipping not embedded file:", gzname)
		return nil
	}
	logrus.Debugf("%s is at offset %d", gzname, bin.offset)

	if !EmbeddedBinaryNeedsUpdate(exinfo, p, bin.originalSize) {
		logrus.Debug("Re-use existing file:", p)
		return nil
	}

	infile, err := os.Open(selfexe)
	if err != nil {
		return fmt.Errorf("unable to open executable: %w", err)
	}
	defer infile.Close()

	// find location at EOF - BinDataSize + offs
	if _, err := infile.Seek(-BinDataSize+bin.offset, 2); err != nil {
		return fmt.Errorf("failed to find embedded file position for '%s': %w", p, err)
	}
	gz, err := gzip.NewReader(io.LimitReader(infile, bin.size))
	if err != nil {
		return fmt.Errorf("failed to create gzip reader for '%s': %w", p, err)
	}

	logrus.Debugf("Writing static file: '%s'", p)

	return file.AtomicWithTarget(p).
		WithPermissions(perm).
		// In order to properly determine if an update of an embedded binary
		// file is needed, the staged embedded binary needs to have the same
		// modification time as the `k0s` executable.
		WithModificationTime(exinfo.ModTime()).
		Do(func(dst file.AtomicWriter) error {
			_, err := io.Copy(dst, gz)
			return err
		})
}
