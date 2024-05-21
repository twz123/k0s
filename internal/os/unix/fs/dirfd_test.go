//go:build unix

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

package unix_test

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	unixfs "github.com/k0sproject/k0s/internal/os/unix/fs"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirFD_NotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foo")

	d, err := unixfs.OpenDir(path)
	if err == nil {
		assert.NoError(t, d.Close())
	}
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestDirFD_Empty(t *testing.T) {
	path := t.TempDir()

	d, err := unixfs.OpenDir(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })

	_, err = fs.ReadFile(d, "foo")
	assert.ErrorIs(t, err, fs.ErrNotExist)

	if entries, err := d.ReadDir("."); assert.NoError(t, err) {
		assert.Empty(t, entries)
	}

	_, err = fs.ReadFile(d, "/no/abs/paths")
	assert.ErrorContains(t, err, "openat /no/abs/paths: invalid argument: path", "Should disallow absolute paths")
}

func TestDirFD_Filled(t *testing.T) {
	dirPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dirPath, "foo"), []byte("lorem"), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(dirPath, "bar"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dirPath, "bar", "baz"), []byte("ipsum"), 0400))

	d, err := unixfs.OpenDir(dirPath)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })

	// Read foo and match contents.
	if data, err := fs.ReadFile(d, "foo"); assert.NoError(t, err) {
		assert.Equal(t, []byte("lorem"), data)
	}

	// Stat foo and match contents.
	if stat, err := fs.Stat(d, "foo"); assert.NoError(t, err) {
		assert.Equal(t, "foo", stat.Name())
		assert.Equal(t, int64(5), stat.Size())
		assert.Equal(t, fs.FileMode(0644), stat.Mode())
		assert.False(t, stat.IsDir())
	}

	// Stat bar and match contents.
	if stat, err := fs.Stat(d, "bar"); assert.NoError(t, err) {
		assert.Equal(t, "bar", stat.Name())
		assert.Greater(t, stat.Size(), int64(0))
		assert.Equal(t, fs.FileMode(0755)|fs.ModeDir, stat.Mode())
		assert.True(t, stat.IsDir())
	}

	// List directory contents and match for correctness.
	if entries, err := d.ReadDir("."); assert.NoError(t, err) && assert.Len(t, entries, 2) {
		slices.SortFunc(entries, func(l, r fs.DirEntry) int {
			return strings.Compare(l.Name(), r.Name())
		})
		assert.Equal(t, "bar", entries[0].Name())
		assert.True(t, entries[0].IsDir(), "dir bar")
		assert.Equal(t, "foo", entries[1].Name())
		assert.False(t, entries[1].IsDir(), "file foo")
	}

	// List directory contents of bar and match for correctness.
	if entries, err := d.ReadDir("bar"); assert.NoError(t, err) && assert.Len(t, entries, 1) {
		assert.Equal(t, "baz", entries[0].Name())
		assert.False(t, entries[0].IsDir(), "file bar/baz")
	}

	// Read bar/baz and match contents.
	if data, err := fs.ReadFile(d, path.Join("bar", "baz")); assert.NoError(t, err) {
		assert.Equal(t, []byte("ipsum"), data)
	}
}
