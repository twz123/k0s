//go:build hack
// +build hack

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

package genbindata

import (
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"go.uber.org/multierr"
)

type fileInfo struct {
	Name                       string
	Path                       string
	TempFile                   string
	Offset, Size, OriginalSize int64
}

type fileInfos []fileInfo

func (f fileInfos) removeTempFiles() (err error) {
	for _, info := range f {
		if rmErr := os.Remove(info.TempFile); rmErr != nil && !os.IsNotExist(rmErr) {
			err = multierr.Append(err, rmErr)
		}
	}
	return
}

func compressFiles(dirs []string, prefix string) (_ fileInfos, err error) {
	var tmpFiles fileInfos
	defer func() {
		if err != nil {
			err = multierr.Append(err, tmpFiles.removeTempFiles())
		}
	}()

	// compress the files
	var wg errWaitGroup
	for _, dir := range dirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			info, err := f.Info()
			if err != nil {
				return nil, err
			}
			filePath := path.Join(dir, f.Name())
			name := strings.TrimPrefix(filePath, prefix) + ".gz"
			tmpf, err := os.CreateTemp("", f.Name())
			if err != nil {
				return nil, err
			}

			tmpFiles = append(tmpFiles, fileInfo{
				Name:         name,
				Path:         filePath,
				TempFile:     tmpf.Name(),
				OriginalSize: info.Size(),
			})

			wg.Go(func() (err error) {
				defer multierr.AppendInvoke(&err, multierr.Close(tmpf))
				gz, err := gzip.NewWriterLevel(tmpf, gzip.BestCompression)
				if err != nil {
					return err
				}
				defer multierr.AppendInvoke(&err, multierr.Close(gz))

				inf, err := interruptibleReadCloserFrom(func() (io.ReadCloser, error) { return os.Open(filePath) }, wg.Err)
				if err != nil {
					return err
				}
				defer multierr.AppendInvoke(&err, multierr.Close(inf))

				size, err := io.Copy(gz, inf)
				if err != nil {
					return err
				}

				fi, err := tmpf.Stat()
				if err != nil {
					return err
				}

				fmt.Fprintf(os.Stderr, "%s: %d/%d MiB\n", name, fi.Size()/(1024*1024), size/(1024*1024))
				return nil
			})
		}
	}

	err = wg.Wait()
	if err != nil {
		return nil, err
	}

	return tmpFiles, nil
}

func GenBindata(name string, args ...string) (err error) {
	var prefix, pkg, outfile, gofile string

	var bindata []fileInfo

	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(new(strings.Builder))
	flags.StringVar(&prefix, "prefix", "", "Optional path prefix to strip off asset names.")
	flags.StringVar(&pkg, "pkg", "main", "Package name to use in the generated code.")
	flags.StringVar(&outfile, "o", "./bindata", "Optional name of the output file to be generated.")
	flags.StringVar(&gofile, "gofile", "./bindata.go", "Optional name of the go file to be generated.")
	err = flags.Parse(args)
	if err != nil || flags.NArg() == 0 {
		buf := flags.Output().(*strings.Builder)
		if err != nil {
			fmt.Fprintln(buf, "Error:", err)
		}
		fmt.Fprintf(buf, "Usage: %s [options] <directories>\n", name)
		flags.PrintDefaults()
		return errors.New(buf.String())
	}

	tmpFiles, err := compressFiles(flags.Args(), prefix)
	if err != nil {
		return err
	}
	defer multierr.AppendInvoke(&err, multierr.Invoke(tmpFiles.removeTempFiles))

	var offset int64
	err = file.WriteAtomically(outfile, 0644, func(outf io.Writer) error {
		fmt.Fprintf(os.Stderr, "Writing %s...\n", outfile)
		for _, t := range tmpFiles {
			err := func() (err error) {
				inf, err := os.Open(t.TempFile)
				if err != nil {
					return err
				}
				defer multierr.AppendInvoke(&err, multierr.Close(inf))

				size, err := io.Copy(outf, inf)
				if err != nil {
					return err
				}

				t.Offset = offset
				t.Size = size
				bindata = append(bindata, t)

				offset += size
				return nil
			}()
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	err = packageTemplate.Execute(&buf, struct {
		OutFile     string
		Pkg         string
		BinData     []fileInfo
		BinDataSize int64
	}{
		OutFile:     outfile,
		Pkg:         pkg,
		BinData:     bindata,
		BinDataSize: offset,
	})
	if err != nil {
		return err
	}

	return file.WriteContentAtomically(gofile, buf.Bytes(), 0644)
}

type errWaitGroup struct {
	wg     sync.WaitGroup
	errPtr atomic.Pointer[error]
}

func (g *errWaitGroup) Go(fn func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		err := fn()
		if err == nil {
			return
		}
		for {
			prevErr := g.errPtr.Load()
			if prevErr != nil {
				if errors.Is(err, errInterrupted) {
					return
				}
				err = multierr.Append(*prevErr, err)
			}
			if g.errPtr.CompareAndSwap(prevErr, &err) {
				return
			}
		}
	}()
}

func (g *errWaitGroup) Err() error {
	if g.errPtr.Load() != nil {
		return errInterrupted
	}

	return nil
}

func (g *errWaitGroup) Wait() error {
	g.wg.Wait()
	errPtr := g.errPtr.Load()
	if errPtr != nil {
		return *errPtr
	}

	return nil
}

var errInterrupted = errors.New("interrupted")

type interruptibleReadCloser struct {
	inner       io.ReadCloser
	interrupted func() error
}

func interruptibleReadCloserFrom(open func() (io.ReadCloser, error), interrupted func() error) (*interruptibleReadCloser, error) {
	inner, err := open()
	if err != nil {
		return nil, err
	}

	return &interruptibleReadCloser{inner, interrupted}, nil
}

func (r *interruptibleReadCloser) Read(bytes []byte) (int, error) {
	if err := r.interrupted(); err != nil {
		return 0, err
	}
	return r.inner.Read(bytes)
}

func (r *interruptibleReadCloser) Close() error {
	return r.inner.Close()
}

var packageTemplate = template.Must(template.New("").Parse(`// Code generated by go generate; DO NOT EDIT.

// datafile: {{ .OutFile }}

package {{ .Pkg }}

var (
	BinData = map[string]struct{ offset, size, originalSize int64 }{
	{{- range .BinData }}
		"{{ .Name }}": { {{ .Offset }}, {{ .Size }}, {{ .OriginalSize }}},
	{{- end }}
	}

	BinDataSize int64 = {{ .BinDataSize }}
)
`))
