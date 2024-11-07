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

package cleanup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/mount-utils"
)

func TestRemoveAllSkipMountPoints_NonExistent(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	hook := test.NewLocal(log)
	dir := t.TempDir()

	removed := removeAllSkipMountPoints(log, mount.New(""), filepath.Join(dir, "non-existent"))
	assert.True(t, removed)
	assert.Empty(t, hook.AllEntries())
}

func TestRemoveAllSkipMountPoints_Symlinks(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	hook := test.NewLocal(log)
	unrelatedDir := t.TempDir()
	removeDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(unrelatedDir, "regular_file"), nil, 0644))
	require.NoError(t, os.Mkdir(filepath.Join(unrelatedDir, "regular_dir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(unrelatedDir, "regular_dir", "some_file"), nil, 0644))

	require.NoError(t, os.WriteFile(filepath.Join(removeDir, "regular_file"), nil, 0644))
	require.NoError(t, os.Mkdir(filepath.Join(removeDir, "regular_dir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(removeDir, "regular_dir", "some_file"), nil, 0644))
	require.NoError(t, os.Mkdir(filepath.Join(removeDir, "regular_empty_dir"), 0755))

	require.NoError(t, os.Symlink(filepath.Join(unrelatedDir, "regular_file"), filepath.Join(removeDir, "symlinked_file")))
	require.NoError(t, os.Symlink(filepath.Join(unrelatedDir, "regular_dir"), filepath.Join(removeDir, "symlinked_dir")))

	removed := removeAllSkipMountPoints(log, mount.New(""), removeDir)
	assert.True(t, removed)
	assert.Empty(t, hook.AllEntries())
	assert.NoDirExists(t, removeDir)
	assert.FileExists(t, filepath.Join(unrelatedDir, "regular_file"))
	assert.DirExists(t, filepath.Join(unrelatedDir, "regular_dir"))
}
