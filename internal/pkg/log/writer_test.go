// SPDX-FileCopyrightText: 2023 k0s authors
// SPDX-License-Identifier: Apache-2.0

package log

import (
	"io/fs"
	"testing"

	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriter(t *testing.T) {
	type entry struct {
		chunk uint
		msg   string
	}

	for _, test := range []struct {
		name                    string
		chunkSize               int
		in                      []string
		beforeClose, afterClose []entry
	}{
		{"empty_write", 3,
			[]string{""},
			[]entry{},
			nil},
		{"single_line", 3,
			[]string{"ab\n"},
			[]entry{{0, "ab"}},
			nil},
		{"exact_lines", 3,
			[]string{"abc\n", "def\n"},
			[]entry{{1, "abc"}, {1, "def"}},
			nil},
		{"multi_line", 3,
			[]string{"ab\ncd\n"},
			[]entry{{0, "ab"}, {0, "cd"}},
			nil},
		{"overlong_lines", 3,
			[]string{"abcd\nef\n"},
			[]entry{{1, "abc"}, {2, "d"}, {0, "ef"}},
			nil},
		{"overlong_lines_2", 3,
			[]string{"abcd\ne", "f", "\n"},
			[]entry{{1, "abc"}, {2, "d"}, {0, "ef"}},
			nil},
		{"unterminated_consecutive_writes_4", 3,
			[]string{"ab", "cd"},
			[]entry{{1, "abc"}},
			[]entry{{2, "d"}}},
		{"unterminated_consecutive_writes_6", 3,
			[]string{"ab", "cd", "ef"},
			[]entry{{1, "abc"}, {2, "def"}},
			nil},
		{"unterminated_consecutive_writes_8", 3,
			[]string{"ab", "cd", "ef", "gh"},
			[]entry{{1, "abc"}, {2, "def"}},
			[]entry{{3, "gh"}}},
		{"unterminated_consecutive_writes_10", 3,
			[]string{"ab", "cd", "ef", "gh", "ij"},
			[]entry{{1, "abc"}, {2, "def"}, {3, "ghi"}},
			[]entry{{4, "j"}}},
		{"long_buffer_short_lines", 16,
			[]string{"a\nb\nc\n"},
			[]entry{{0, "a"}, {0, "b"}, {0, "c"}},
			nil},
		{"utf8", 26, // would split after the third byte of 🫣
			[]string{"this is four bytes: >>>🫣\n<<<\n"},
			[]entry{{1, "this is four bytes: >>>"}, {2, "🫣"}, {0, "<<<"}},
			nil},
		{"strips_carriage_returns", 5,
			[]string{"abc\r\ndef\r\n"},
			[]entry{{0, "abc"}, {0, "def"}},
			nil},
		{"unterminated_line", 16,
			[]string{"no newline"},
			nil,
			[]entry{{0, "no newline"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			log, logs := logtest.NewNullLogger()
			underTest := NewWriter(log, test.chunkSize)

			for _, line := range test.in {
				_, err := underTest.Write([]byte(line))
				require.NoError(t, err)
			}

			assertChunks := func(t *testing.T, expected []entry) {
				remaining := logs.AllEntries()
				logs.Reset()

				for i, line := range expected {
					if !assert.NotEmptyf(t, remaining, "Expected additional log entry: %v", line) {
						continue
					}

					chunk, isChunk := remaining[0].Data["chunk"]
					assert.Equalf(t, line.chunk != 0, isChunk, "Log entry %d chunk mismatch", i)
					if isChunk {
						assert.Equalf(t, line.chunk, chunk, "Log entry %d differs in chunk", i)
					}

					assert.Equalf(t, line.msg, remaining[0].Message, "Log entry %d differs in message", i)
					remaining = remaining[1:]
				}

				for _, entry := range remaining {
					assert.Failf(t, "Unexpected log entry", "%s", entry.Message)
				}
			}

			t.Run("before_close", func(t *testing.T) { assertChunks(t, test.beforeClose) })

			assert.NoError(t, underTest.Close())

			t.Run("after_close", func(t *testing.T) { assertChunks(t, test.afterClose) })

			assert.Same(t, fs.ErrClosed, underTest.Close())
			n, err := underTest.Write([]byte("closed?"))
			assert.Zero(t, n)
			assert.Same(t, fs.ErrClosed, err)
			assert.Empty(t, logs.AllEntries())
		})
	}
}
