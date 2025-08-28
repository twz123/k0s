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
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	osunix "github.com/k0sproject/k0s/internal/os/unix"
	"golang.org/x/sys/unix"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirFD_NotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foo")

	d, err := osunix.OpenDir(path, 0)
	if err == nil {
		assert.NoError(t, d.Close())
	}
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestDirFD_Empty(t *testing.T) {
	path := t.TempDir()

	d, err := osunix.OpenDir(path, 0)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })

	foo := "foo"
	assertENOENT := func(t *testing.T, op string, err error) {
		var pathErr *os.PathError
		if assert.ErrorAs(t, err, &pathErr) {
			assert.Equal(t, op, pathErr.Op)
			assert.Equal(t, foo, pathErr.Path)
			assert.Equal(t, syscall.ENOENT, pathErr.Err)
		}
	}

	_, err = d.StatAt(foo, 0)
	assertENOENT(t, "fstatat", err)

	_, err = d.ReadFile(foo)
	assertENOENT(t, "openat", err)
}

func TestDirFD_Filled(t *testing.T) {
	dirPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dirPath, "foo"), []byte("lorem"), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(dirPath, "bar"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dirPath, "bar", "baz"), []byte("ipsum"), 0400))

	now := time.Now()
	require.NoError(t, os.Chtimes(filepath.Join(dirPath, "foo"), time.Time{}, now.Add(-3*time.Minute)))
	require.NoError(t, os.Chtimes(filepath.Join(dirPath, "bar", "baz"), time.Time{}, now.Add(-2*time.Minute)))
	require.NoError(t, os.Chtimes(filepath.Join(dirPath, "bar"), time.Time{}, now.Add(-1*time.Minute)))

	d, err := osunix.OpenDir(dirPath, 0)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })

	// Read foo and match contents.
	if data, err := d.ReadFile("foo"); assert.NoError(t, err) {
		assert.Equal(t, []byte("lorem"), data)
	}

	// Stat foo and match contents.
	if stat, err := d.StatAt("foo", 0); assert.NoError(t, err) {
		assert.Equal(t, "foo", stat.Name())
		assert.Equal(t, int64(5), stat.Size())
		assert.WithinDuration(t, now.Add(-3*time.Minute), stat.ModTime(), 0)
		assert.Equal(t, os.FileMode(0644), stat.Mode())
		assert.False(t, stat.IsDir())
		assert.IsType(t, new(unix.Stat_t), stat.Sys())
	}

	// Stat bar and match contents.
	if stat, err := d.StatAt("bar", 0); assert.NoError(t, err) {
		assert.Equal(t, "bar", stat.Name())
		assert.Positive(t, stat.Size())
		assert.WithinDuration(t, now.Add(-1*time.Minute), stat.ModTime(), 0)
		assert.Equal(t, os.FileMode(0755)|os.ModeDir, stat.Mode())
		assert.True(t, stat.IsDir())
		assert.IsType(t, new(unix.Stat_t), stat.Sys())
	}

	// Stat bar/baz and match contents.
	if stat, err := d.StatAt(filepath.Join("bar", "baz"), 0); assert.NoError(t, err) {
		assert.Equal(t, "baz", stat.Name())
		assert.Equal(t, int64(5), stat.Size())
		assert.WithinDuration(t, now.Add(-2*time.Minute), stat.ModTime(), 0)
		assert.Equal(t, os.FileMode(0400), stat.Mode())
		assert.False(t, stat.IsDir())
		assert.IsType(t, new(unix.Stat_t), stat.Sys())
	}

	// Read bar/baz and match contents.
	if data, err := d.ReadFile(filepath.Join("bar", "baz")); assert.NoError(t, err) {
		assert.Equal(t, []byte("ipsum"), data)
	}
}
