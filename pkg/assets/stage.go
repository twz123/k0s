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
	"archive/zip"
	"compress/bzip2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/sirupsen/logrus"
	"github.com/ulikunitz/xz/lzma"
)

// EmbeddedBinaryNeedsUpdate returns true if the provided embedded binary file should
// be updated. This determination is based on the modification times and file sizes of both
// the provided executable and the embedded executable. It is expected that the embedded binary
// modification times should match the main `k0s` executable.
func EmbeddedBinaryNeedsUpdate(embeddedBinaryPath string, modTime time.Time, size int64) bool {
	if pathinfo, err := os.Stat(embeddedBinaryPath); err == nil {
		return !modTime.Equal(pathinfo.ModTime()) || pathinfo.Size() != size
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
	// Look into the BinDir folder.
	path := filepath.Join(binDir, name)
	if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
		return path
	}

	// If we still haven't found the executable, look for it in the PATH.
	if path, err := exec.LookPath(name); err == nil {
		path, _ := filepath.Abs(path)
		return path
	}
	return name
}

// Stage ...
func Stage(dataDir string, name string, dirMode os.FileMode) error {
	p := filepath.Join(dataDir, name)
	logrus.Infof("Staging %q", p)

	err := dir.Init(filepath.Dir(p), dirMode)
	if err != nil {
		return fmt.Errorf("failed to create dir %q: %w", filepath.Dir(p), err)
	}

	selfexe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("unable to determine current executable: %w", err)
	}

	var modTime time.Time
	{
		exinfo, err := os.Stat(selfexe)
		if err != nil {
			return fmt.Errorf("while getting modification time: %w", err)
		}
		modTime = exinfo.ModTime()
	}

	zipFile, err := zip.OpenReader(selfexe)
	if err != nil {
		return fmt.Errorf("while staging %q: %w", name, err)
	}
	defer func() { err = errors.Join(err, zipFile.Close()) }()

	zipFile.RegisterDecompressor( /* bzip2: */ 12, func(r io.Reader) io.ReadCloser {
		return io.NopCloser(bzip2.NewReader(r))
	})
	zipFile.RegisterDecompressor( /* lzma: */ 14, func(r io.Reader) io.ReadCloser {
		r, err := lzma.NewReader(r)
		if err != nil {
			return io.NopCloser(&deferredReaderError{err})
		}
		return io.NopCloser(r)
	})

	var (
		fileToExtract *zip.File
		fileInfo      os.FileInfo
	)
	for _, archivedFile := range zipFile.File {
		if archivedFile.Name == name {
			fileToExtract = archivedFile
			fileInfo = fileToExtract.FileInfo()
			break
		}
	}
	if fileToExtract == nil || fileInfo.IsDir() {
		logrus.Debug("Skipping not embedded file:", name)
		return nil
	}

	if !EmbeddedBinaryNeedsUpdate(p, modTime, fileInfo.Size()) {
		logrus.Debug("Re-use existing file:", p)
		return nil
	}

	// Get a reader for the uncompressed file contents
	contents, err := fileToExtract.Open()
	if err != nil {
		if err == zip.ErrAlgorithm {
			err = fmt.Errorf("%w: %d", err, fileToExtract.Method)
		}
		return fmt.Errorf("while extracting %q: %w", p, err)
	}
	defer func() { err = errors.Join(err, contents.Close()) }()

	logrus.Debugf("Writing static file: %q", p)

	// Write the contents to the destination file.
	if err = file.WriteAtomically(p, 0550, func(file io.Writer) error {
		// Actually extract the data.
		bytesWritten, err := io.Copy(file, contents)
		if err != nil {
			return fmt.Errorf("while extracting %q: %w", p, err)
		}
		// Just a quick sanity check. The CRC32 checks will be performed inside
		// the io.ReadCloser returned by the zip package, so no need to do it
		// twice.
		if size := fileInfo.Size(); bytesWritten != size {
			return fmt.Errorf("file size mismatch for %q: want %d, got %d", p, size, bytesWritten)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("while extracting %q: %w", p, err)
	}

	// In order to properly determine if an update of an embedded binary file is needed,
	// the staged embedded binary needs to have the same modification time as the `k0s`
	// executable.
	if err := os.Chtimes(p, modTime, modTime); err != nil {
		return fmt.Errorf("while extracting %q: %w", p, err)
	}
	return nil
}

type deferredReaderError struct{ err error }

func (d *deferredReaderError) Read(p []byte) (n int, err error) { return 0, d.err }
