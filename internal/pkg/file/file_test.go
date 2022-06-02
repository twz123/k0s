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
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExists(t *testing.T) {
	dir := t.TempDir()

	// test no-existing
	got := Exists(filepath.Join(dir, "non-existing"))
	want := false
	if got != want {
		t.Errorf("test non-existing: got %t, wanted %t", got, want)
	}

	existingFileName := filepath.Join(dir, "existing")
	require.NoError(t, os.WriteFile(existingFileName, []byte{}, 0644))

	// test existing
	got = Exists(existingFileName)
	want = true
	if got != want {
		t.Errorf("test existing tempfile %s: got %t, wanted %t", existingFileName, got, want)
	}

	// test what happens when we don't have permissions to the directory to file
	// and can confirm that it actually exists
	if assert.NoError(t, os.Chmod(dir, 0000)) {
		t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	}

	got = Exists(existingFileName)
	want = false
	if got != want {
		t.Errorf("test existing tempfile %s: got %t, wanted %t", existingFileName, got, want)
	}

}

func TestIsDottedBaseName(t *testing.T) {
	roots := []struct {
		name   string
		dotted DottedBaseName
		prefix string
	}{
		{"absolute", NotDotted, "/rooted/path/"},
		{"relative", RelativeBase, ""},
	}

	if runtime.GOOS == "windows" {
		roots[0].prefix = `C:\rooted\path\`
	}

	paths := []struct {
		name, path string
		dotted     DottedBaseName
		skip       int
	}{
		{"Empty", "", NotDotted, 1},
		{"Dot", ".", RelativeBase, 1},
		{"DotDot", "..", RelativeBase, 2},
		{"DotDotDot", "...", Dotted, 0},
		{"A", "A", NotDotted, 0},
		{"DotA", ".A", Dotted, 0},
		{"ADot", "A.", NotDotted, 0},
	}

	assert.Equal(t, "...", filepath.Join("...", "."))
	assert.Equal(t, "...", filepath.Base("..."))

	for _, root := range roots {
		for _, dir := range paths {
			for _, file := range paths {
				t.Run(fmt.Sprintf("%s_%sDir_%sFile", root.name, dir.name, file.name), func(t *testing.T) {
					expectedDotted := file.dotted
					if file.skip > 0 {
						expectedDotted = dir.dotted
						if file.skip+dir.skip > 1 {
							expectedDotted = root.dotted
						}
					}

					path := root.prefix
					if dir.path != "" {
						path += dir.path
						path += string(filepath.Separator)
					}
					path += file.path

					dotted := IsDottedBaseName(path)
					t.Logf("%v := IsDottedBaseName(%q)", dotted, path)
					if expectedDotted != dotted {
						assert.Fail(t, dotted.String(), "Expected %q to be %v", path, expectedDotted)
					}
				})
			}
		}
	}
}
