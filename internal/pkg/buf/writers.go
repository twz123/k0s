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

package buf

import (
	"bytes"
	"io"
)

type LineWriter struct {
	WriteLine func(line []byte) error
	buf       bytes.Buffer
}

var _ io.Writer = (*LineWriter)(nil)

func (w *LineWriter) Write(p []byte) (int, error) {
	rest := p
	for {
		var line []byte
		var found bool
		line, rest, found = bytes.Cut(rest, []byte("\n"))
		if found {
			line = bytes.TrimRight(line, "\r")
		}
		w.buf.Write(line)
		if !found {
			break
		}

		if err := w.Flush(true); err != nil {
			return 0, err
		}
	}

	return len(p), nil
}

func (w *LineWriter) Flush(force bool) error {
	if force || w.buf.Len() > 0 {
		err := w.WriteLine(w.buf.Bytes())
		w.buf.Reset()
		return err
	}
	return nil
}
