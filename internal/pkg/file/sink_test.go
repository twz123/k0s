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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFsDirSink(t *testing.T) {
	tmpDir := t.TempDir()
	sinkDir := filepath.Join(tmpDir, "sink")

	var underTest Sink

	t.Run("New", func(t *testing.T) {
		var err error

		verifyRootDir := func(*testing.T) {
			stat, err := os.Stat(sinkDir)
			require.NoError(t, err)
			assert.True(t, stat.IsDir())
			assert.Equal(t, os.FileMode(0700), stat.Mode()&0777)
		}

		t.Run("Creates_RootDir", func(t *testing.T) {
			_, err = NewFsDirSink(sinkDir, 0700, 0600)
			assert.NoError(t, err)
			verifyRootDir(t)
		})

		t.Run("Chmods_Exisiting_RootDir", func(t *testing.T) {
			assert.NoError(t, os.Chmod(sinkDir, 0777))
			underTest, err = NewFsDirSink(sinkDir, 0700, 0600)
			assert.NoError(t, err)
			verifyRootDir(t)
		})

		t.Run("Fails_On_Obstructed_RoootDir", func(t *testing.T) {
			obstructedPath := filepath.Join(tmpDir, "obstructed")
			require.NoError(t, os.WriteFile(obstructedPath, []byte("obstructed"), 0666))

			_, err = NewFsDirSink(obstructedPath, 0700, 0600)
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), "failed to initialize root directory")
			}
		})
	})

	const expectedContent = "good content"

	t.Run("Save", func(t *testing.T) {

		assertFileIsValid := func(t *testing.T, theFile string) {
			stat, err := os.Stat(theFile)
			require.NoError(t, err)
			assert.False(t, stat.IsDir())
			assert.Equal(t, os.FileMode(0600), stat.Mode()&0777)

			content, err := os.ReadFile(theFile)
			require.NoError(t, err)
			assert.Equal(t, expectedContent, string(content))
		}

		theFile := filepath.Join(sinkDir, "theFile")

		t.Run("Create", func(t *testing.T) {
			require.NoError(t, underTest.Save("theFile", []byte(expectedContent)))
			assertFileIsValid(t, theFile)
		})

		t.Run("NoOverwrite", func(t *testing.T) {
			err := underTest.Save("theFile", []byte("garbage"))
			if assert.Error(t, err) {
				t.Logf("%#v", err)

				assert.Contains(t, err.Error(), `failed to create "theFile": `)

				rootCause := err
				for {
					cause := errors.Unwrap(rootCause)
					if cause == nil {
						break
					}
					rootCause = cause
				}

				assert.True(t, os.IsExist(rootCause))
			}

			assertFileIsValid(t, theFile)
		})

		t.Run("Subdir", func(t *testing.T) {
			theSubdir := filepath.Join(sinkDir, "subdir")
			theFile := filepath.Join(theSubdir, "theFile")

			err := underTest.Save(filepath.Join("subdir", "theFile"), []byte(expectedContent))

			stat, err := os.Stat(theSubdir)
			require.NoError(t, err)
			assert.True(t, stat.IsDir())
			assert.Equal(t, os.FileMode(0700), stat.Mode()&0777)

			assertFileIsValid(t, theFile)
		})

		t.Run("Enforce_RootDir", func(t *testing.T) {
			for _, test := range []struct {
				name, fileName string
			}{
				{"Empty", ""},
				{"Dot", "."},
				{"DotDot", ".."},
				{"DotDot_Escape", filepath.Join("..", "escapist")},
				// Assuming that os.TempDir() is an absolute path...
				{"Absolute_FileName", filepath.Join(os.TempDir(), "escapist")},
			} {
				t.Run(test.name, func(t *testing.T) {
					err := underTest.Save(test.fileName, []byte("garbage"))
					if assert.Error(t, err) {
						assert.Equal(t, err.Error(), fmt.Sprintf("not a relative file name: %q", test.fileName))
					}
				})
			}
		})
	})
}
