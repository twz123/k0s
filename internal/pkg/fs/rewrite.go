package fs

import (
	iofs "io/fs"
)

type NamedFile struct {
	Inner iofs.File
	Name  string
}

var _ iofs.File = (*NamedFile)(nil)

// Close implements fs.File.
func (n *NamedFile) Close() error {
	// FIXME are there PathErrors?
	return n.Inner.Close()
}

// Read implements fs.File.
func (n *NamedFile) Read([]byte) (int, error) {
	panic("unimplemented")
}

// Stat implements fs.File.
func (n *NamedFile) Stat() (iofs.FileInfo, error) {
	panic("unimplemented")
}

type NamedFileInfo struct {
	Inner iofs.FileInfo
	Name  string
}

type RewriteFS struct {
	FS       iofs.FS
	From, To func(string) (string, error)
}

func (fs *RewriteFS) Open(name string) (iofs.File, error) {
	// FIXME can we pull this into rewritePathErrors as well?
	fsName, err := fs.To(name)
	if err != nil {
		return nil, err
	}

	return rewritePathErrors(name, fsName, func() (iofs.File, error) {
		return fs.FS.Open(fsName)
	})
}

func rewritePathErrors[T any](outer, inner string, f func() (T, error)) (t T, err error) {
	t, err = f()
	if err == nil {
		return t, nil
	}

	pathErr, ok := err.(*iofs.PathError)
	if !ok || pathErr.Path != inner {
		return t, err
	}

	rewrittenErr := *pathErr
	rewrittenErr.Path = outer
	return t, &rewrittenErr
}
