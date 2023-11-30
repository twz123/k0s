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

package dir_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExists(t *testing.T) {
	t.Run("existing_directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		exists, err := dir.Exists(tmpDir)
		if assert.NoError(t, err) {
			assert.True(t, exists, "Freshly created temp dir should exist")
		}
	})

	t.Run("nonexistent_path", func(t *testing.T) {
		tmpDir := t.TempDir()
		p := filepath.Join(tmpDir, "some-path")

		exists, err := dir.Exists(p)
		if assert.NoError(t, err) {
			assert.False(t, exists, "Nonexistent path shouldn't exist")
		}
	})

	t.Run("empty_path", func(t *testing.T) {
		tmpDir := t.TempDir()
		defer testutil.Chdir(t, tmpDir)()
		_, err := dir.Exists("")
		assert.ErrorIs(t, err, os.ErrNotExist)
		assert.ErrorContains(t, err, "try a dot instead of an empty path")

		exists, err := dir.Exists(".")
		if assert.NoError(t, err) {
			assert.True(t, exists, "Current directory should exist")
		}
	})

	t.Run("obstructed_path", func(t *testing.T) {
		tmpDir := t.TempDir()
		p := filepath.Join(tmpDir, "some-path")
		require.NoError(t, os.WriteFile(p, []byte("obstructed"), 0644))

		exists, err := dir.Exists(p)
		if assert.NoError(t, err) {
			assert.False(t, exists, "Obstructed path shouldn't exist")
		}

		exists, err = dir.Exists(filepath.Join(p, "sub-path"))
		if assert.NoError(t, err) {
			assert.False(t, exists, "Obstructed sub-path shouldn't exist")
		}
	})

	t.Run("dangling_symlink", func(t *testing.T) {
		tmpDir := t.TempDir()
		nonexistent := filepath.Join(tmpDir, "nonexistent")
		dangling := filepath.Join(tmpDir, "dangling")
		require.NoError(t, os.Symlink(nonexistent, dangling))

		exists, err := dir.Exists(dangling)
		if assert.NoError(t, err) {
			assert.False(t, exists, "Dangling symlink shouldn't exist")
		}

		require.NoError(t, os.Mkdir(nonexistent, 0755))

		exists, err = dir.Exists(dangling)
		if assert.NoError(t, err) {
			assert.True(t, exists, "Symlinked directory should exist")
		}
	})

	t.Run("long_path", func(t *testing.T) {
		tmpDir := t.TempDir()
		// The upper limit is 255 for virtually all OSes/FSes
		longPath := strings.Repeat("x", 256)
		_, err := dir.Exists(filepath.Join(tmpDir, longPath))
		assert.ErrorIs(t, err, syscall.ENAMETOOLONG)
	})
}

func TestGetAll(t *testing.T) {
	t.Run("empty", func(t *testing.T) {

		tmpDir := t.TempDir()
		dirs, err := dir.GetAll(tmpDir)
		require.NoError(t, err)
		require.Empty(t, dirs)
	})
	t.Run("filter dirs", func(t *testing.T) {
		tmpDir := t.TempDir()
		require.NoError(t, dir.Init(filepath.Join(tmpDir, "dir1"), 0750))
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "file1"), []byte{}, 0600), "Unable to create file %s:", "file1")
		dirs, err := dir.GetAll(tmpDir)
		require.NoError(t, err)
		require.Equal(t, []string{"dir1"}, dirs)
	})
}
