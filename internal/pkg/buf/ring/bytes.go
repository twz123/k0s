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

package ring

import (
	"io"
)

// ByteBuffer is a [Buffer] of bytes that implements [io.Writer].
type ByteBuffer struct{ *Buffer[byte] }

var _ io.Writer = (*ByteBuffer)(nil)

// Write implements [io.Writer].
func (w *ByteBuffer) Write(p []byte) (n int, err error) {
	w.PushAllBack(p)
	return len(p), nil
}
