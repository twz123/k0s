package util_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/k0sproject/k0s/internal/pkg/fs/util"
)

func TestReadFile(t *testing.T) {
	fsys := util.EmptyFS() // Instantiate your empty, read-only FS

	_, err := fs.ReadFile(fsys, "nonexistent.txt")
	if err == nil {
		t.Error("expected an error for non-existent file, got nil")
	}
}

func TestReadDir(t *testing.T) {
	fsys := util.EmptyFS()

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatalf("failed to read root directory: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("expected no entries in root, found %d", len(entries))
	}
}

func TestOpenFile(t *testing.T) {
	fsys := util.EmptyFS()

	_, err := fsys.Open("nonexistent.txt")
	if err == nil {
		t.Error("expected an error for non-existent file, got nil")
	}
}

func TestWriteFile(t *testing.T) {
	fsys := util.EmptyFS()

	_, err := fsys.Open("file.txt", fs.O_RDWR|fs.O_CREATE, 0666)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("expected a permission error, got %v", err)
	}
}

func TestFileInfo(t *testing.T) {
	fsys := util.EmptyFS()

	_, err := fs.Stat(fsys, "nonexistent.txt")
	if err == nil {
		t.Error("expected an error for non-existent file, got nil")
	}
}

func TestFileMode(t *testing.T) {
	fsys := util.EmptyFS()

	info, err := fs.Stat(fsys, ".")
	if err != nil {
		t.Fatalf("failed to stat root: %v", err)
	}

	if !info.Mode().IsDir() {
		t.Error("root is not a directory")
	}

	// Assuming you have a way to check if the FS is read-only; adjust as necessary
	if !info.Mode().IsReadOnly() {
		t.Error("file system is not read-only")
	}
}
