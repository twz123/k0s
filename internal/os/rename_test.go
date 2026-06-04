// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package os

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenameNoReplace_NewDestination(t *testing.T) {
	dir := t.TempDir()

	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")

	require.NoError(t, os.WriteFile(oldPath, []byte("hello"), 0644))

	err := RenameNoReplace(oldPath, newPath)
	require.NoError(t, err)

	_, err = os.Stat(oldPath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	if got, err := os.ReadFile(newPath); assert.NoError(t, err) {
		assert.Equal(t, []byte("hello"), got)
	}
}

func TestRenameNoReplace_DestinationExists(t *testing.T) {
	dir := t.TempDir()

	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")

	require.NoError(t, os.WriteFile(oldPath, []byte("old contents"), 0644))
	require.NoError(t, os.WriteFile(newPath, []byte("new contents"), 0644))

	err := RenameNoReplace(oldPath, newPath)
	if linkErr := (*os.LinkError)(nil); assert.ErrorAs(t, err, &linkErr) {
		assert.Equal(t, "renameNoReplace", linkErr.Op)
		assert.Equal(t, oldPath, linkErr.Old)
		assert.Equal(t, newPath, linkErr.New)
		if syscallErr := (*os.SyscallError)(nil); assert.ErrorAs(t, linkErr.Err, &syscallErr) {
			assert.ErrorIs(t, syscallErr, os.ErrExist)
		}
	}

	if gotOld, err := os.ReadFile(oldPath); assert.NoError(t, err) {
		assert.Equal(t, []byte("old contents"), gotOld)
	}

	if gotNew, err := os.ReadFile(newPath); assert.NoError(t, err) {
		assert.Equal(t, []byte("new contents"), gotNew)
	}
}

func TestRenameNoReplace_MissingSource(t *testing.T) {
	dir := t.TempDir()

	oldPath := filepath.Join(dir, "missing.txt")
	newPath := filepath.Join(dir, "new.txt")

	err := RenameNoReplace(oldPath, newPath)
	require.NotErrorIs(t, err, os.ErrExist)
	if linkErr := (*os.LinkError)(nil); assert.ErrorAs(t, err, &linkErr) {
		assert.Equal(t, "renameNoReplace", linkErr.Op)
		assert.Equal(t, oldPath, linkErr.Old)
		assert.Equal(t, newPath, linkErr.New)
		if syscallErr := (*os.SyscallError)(nil); assert.ErrorAs(t, linkErr.Err, &syscallErr) {
			assert.ErrorIs(t, syscallErr, os.ErrNotExist)
		}
	}

	_, statErr := os.Stat(newPath)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestRenameNoReplace_DestinationParentMissing(t *testing.T) {
	dir := t.TempDir()

	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "missing-dir", "new.txt")

	require.NoError(t, os.WriteFile(oldPath, []byte("hello"), 0644))

	err := RenameNoReplace(oldPath, newPath)
	assert.NotErrorIs(t, err, os.ErrExist)

	// The error returned has a different structure on Linux and Windows,
	// leaking actionable, platform-specific implementation details. On Linux,
	// one can determine which path (old or new) is missing from the error,
	// which is not possible on Windows. However, stripping or normalizing
	// errors requires more code and may complicate troubleshooting.
	if linkErr := (*os.LinkError)(nil); assert.ErrorAs(t, err, &linkErr) {
		assert.Equal(t, "renameNoReplace", linkErr.Op)
		assert.Equal(t, oldPath, linkErr.Old)
		assert.Equal(t, newPath, linkErr.New)
		assert.ErrorIs(t, linkErr.Err, os.ErrNotExist)
	}

	if got, readErr := os.ReadFile(oldPath); assert.NoError(t, readErr) {
		assert.Equal(t, []byte("hello"), got)
	}
}

func TestRenameNoReplace_SourceParentMissing(t *testing.T) {
	dir := t.TempDir()

	oldPath := filepath.Join(dir, "missing-dir", "old.txt")
	newPath := filepath.Join(dir, "new.txt")

	err := RenameNoReplace(oldPath, newPath)
	assert.NotErrorIs(t, err, os.ErrExist)

	// The error returned has a different structure on Linux and Windows,
	// leaking actionable, platform-specific implementation details. On Linux,
	// one can determine which path (old or new) is missing from the error,
	// which is not possible on Windows. However, stripping or normalizing
	// errors requires more code and may complicate troubleshooting.
	if linkErr := (*os.LinkError)(nil); assert.ErrorAs(t, err, &linkErr) {
		assert.Equal(t, "renameNoReplace", linkErr.Op)
		assert.Equal(t, oldPath, linkErr.Old)
		assert.Equal(t, newPath, linkErr.New)
		assert.ErrorIs(t, linkErr.Err, os.ErrNotExist)
	}

	_, statErr := os.Stat(newPath)
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestRenameNoReplace_DifferentDirectories(t *testing.T) {
	dir := t.TempDir()

	oldDir := filepath.Join(dir, "old-dir")
	newDir := filepath.Join(dir, "new-dir")

	require.NoError(t, os.Mkdir(oldDir, 0755))
	require.NoError(t, os.Mkdir(newDir, 0755))

	oldPath := filepath.Join(oldDir, "file.txt")
	newPath := filepath.Join(newDir, "file.txt")

	require.NoError(t, os.WriteFile(oldPath, []byte("hello"), 0644))

	err := RenameNoReplace(oldPath, newPath)
	require.NoError(t, err)

	_, err = os.Stat(oldPath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	if got, err := os.ReadFile(newPath); assert.NoError(t, err) {
		assert.Equal(t, []byte("hello"), got)
	}
}

func TestRenameNoReplace_DifferentDirectoriesDestinationExists(t *testing.T) {
	dir := t.TempDir()

	oldDir := filepath.Join(dir, "old-dir")
	newDir := filepath.Join(dir, "new-dir")

	require.NoError(t, os.Mkdir(oldDir, 0755))
	require.NoError(t, os.Mkdir(newDir, 0755))

	oldPath := filepath.Join(oldDir, "file.txt")
	newPath := filepath.Join(newDir, "file.txt")

	require.NoError(t, os.WriteFile(oldPath, []byte("old contents"), 0644))
	require.NoError(t, os.WriteFile(newPath, []byte("new contents"), 0644))

	err := RenameNoReplace(oldPath, newPath)
	if linkErr := (*os.LinkError)(nil); assert.ErrorAs(t, err, &linkErr) {
		assert.Equal(t, "renameNoReplace", linkErr.Op)
		assert.Equal(t, oldPath, linkErr.Old)
		assert.Equal(t, newPath, linkErr.New)
		if syscallErr := (*os.SyscallError)(nil); assert.ErrorAs(t, linkErr.Err, &syscallErr) {
			assert.ErrorIs(t, syscallErr, os.ErrExist)
		}
	}

	if gotOld, err := os.ReadFile(oldPath); assert.NoError(t, err) {
		assert.Equal(t, []byte("old contents"), gotOld)
	}

	if gotNew, err := os.ReadFile(newPath); assert.NoError(t, err) {
		assert.Equal(t, []byte("new contents"), gotNew)
	}
}

func TestRenameNoReplace_RelativePaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	require.NoError(t, os.WriteFile("old.txt", []byte("hello"), 0644))

	err := RenameNoReplace("old.txt", "new.txt")
	require.NoError(t, err)

	_, err = os.Stat("old.txt")
	assert.ErrorIs(t, err, os.ErrNotExist)

	if got, err := os.ReadFile("new.txt"); assert.NoError(t, err) {
		assert.Equal(t, []byte("hello"), got)
	}
}

func TestRenameNoReplace_DestinationDirectoryExists(t *testing.T) {
	dir := t.TempDir()

	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "existing-dir")

	require.NoError(t, os.WriteFile(oldPath, []byte("hello"), 0644))
	require.NoError(t, os.Mkdir(newPath, 0755))

	err := RenameNoReplace(oldPath, newPath)
	if linkErr := (*os.LinkError)(nil); assert.ErrorAs(t, err, &linkErr) {
		assert.Equal(t, "renameNoReplace", linkErr.Op)
		assert.Equal(t, oldPath, linkErr.Old)
		assert.Equal(t, newPath, linkErr.New)
		if syscallErr := (*os.SyscallError)(nil); assert.ErrorAs(t, err, &syscallErr) {
			assert.ErrorIs(t, syscallErr, os.ErrExist)
		}
	}

	if got, readErr := os.ReadFile(oldPath); assert.NoError(t, readErr) {
		assert.Equal(t, []byte("hello"), got)
	}

	assert.DirExists(t, newPath)
}

func TestRenameNoReplace_SourceDirectory(t *testing.T) {
	dir := t.TempDir()

	oldPath := filepath.Join(dir, "old-dir")
	newPath := filepath.Join(dir, "new-dir")

	require.NoError(t, os.Mkdir(oldPath, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(oldPath, "child.txt"), []byte("hello"), 0644))

	err := RenameNoReplace(oldPath, newPath)
	require.NoError(t, err)

	_, err = os.Stat(oldPath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	if got, err := os.ReadFile(filepath.Join(newPath, "child.txt")); assert.NoError(t, err) {
		assert.Equal(t, []byte("hello"), got)
	}
}

func TestRenameNoReplace_EmptyPaths(t *testing.T) {
	dir := t.TempDir()

	for _, tt := range []struct {
		name, oldPath, newPath string
	}{
		{"allPathsEmpty", "", ""},
		{"emptyOldPath", "", dir},
		{"emptyNewPath", dir, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := RenameNoReplace(tt.oldPath, tt.newPath)
			if linkErr := (*os.LinkError)(nil); assert.ErrorAs(t, err, &linkErr) {
				assert.Equal(t, "renameNoReplace", linkErr.Op)
				assert.Equal(t, tt.oldPath, linkErr.Old)
				assert.Equal(t, tt.newPath, linkErr.New)
				if syscallErr := (*os.SyscallError)(nil); assert.ErrorAs(t, linkErr.Err, &syscallErr) {
					assert.ErrorIs(t, syscallErr, os.ErrNotExist)
				}
			}
		})
	}
}
