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
	"runtime"
	"testing"

	"github.com/k0sproject/k0s/internal/pkg/dir"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestIsParent(t *testing.T) {
	type test = struct {
		name      string
		basePath  string
		childPath string
		want      bool
	}

	var tests []test
	if runtime.GOOS == "windows" {
		tests = []test{
			{"Direct Parent", `C:\a\b`, `C:\a\b\c`, true},
			{"Indirect Parent", `C:\`, `C:\a\b\c\d`, true},
			{"No Parent Same Drive", `C:\a\b`, `C:\x\y\z`, false},
			{"No Parent Different Drive", `C:\a\b`, `D:\a\b\c`, false},
			{"Same Path", `C:\a\b`, `C:\a\b`, false},
			{"Root Directory", `C:\`, `C:\a\b\c`, true},
			{"Case Insensitivity", `c:\A\B`, `C:\a\b\C`, true},
			{"Mixed Separators", `C:/a/b`, `C:\a\b\c`, true},
			{"Trailing Backslash", `C:\a\b\`, `C:\a\b\c`, true},
		}
	} else {
		tests = []test{
			{"Direct Parent", "/a/b", "/a/b/c", true},
			{"Indirect Parent", "/a", "/a/b/c/d", true},
			{"No Parent", "/a/b", "/x/y/z", false},
			{"Same Path", "/a/b", "/a/b", false},
			{"Root Directory", "/", "/a/b/c", true},
			{"Trailing Slash in Base", "/a/b/", "/a/b/c", true},
			{"Trailing Slash in Child", "/a/b", "/a/b/c/", true},
		}
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t,
				test.want, dir.IsParent(test.basePath, test.childPath),
				"for basePath=%q and childPath=%q", test.basePath, test.childPath,
			)
		})
	}
}
