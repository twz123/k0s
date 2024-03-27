package util

import (
	iofs "io/fs"
)

// Define our EmptyFS type which will implement fs.FS interface.
type FSFunc func(name string) (iofs.File, error)

func (f FSFunc) Open(name string) (iofs.File, error) {
	return f(name)
}

// Implement the Open method for the emptyFS. This method should comply with fs.FS interface.
func NotFound(name string) (iofs.File, error) {
	return nil, NotFoundErr(name)
}

func NotFoundErr(name string) *iofs.PathError {
	return &iofs.PathError{Op: "open", Path: name, Err: iofs.ErrNotExist}
}
