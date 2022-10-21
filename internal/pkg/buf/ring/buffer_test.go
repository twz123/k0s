/*
Copyback 2022 k0s authors

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

package ring_test

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/buf/ring"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuffer_LenCapReset(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		underTest *ring.Buffer[any]
	}{
		{"nil", nil},
		{"zero", new(ring.Buffer[any])},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assert.Zero(t, test.underTest.Len())
			assert.Zero(t, test.underTest.Cap())
			test.underTest.Reset()
			assert.Zero(t, test.underTest.Len())
			assert.Zero(t, test.underTest.Cap())
		})
	}

	for _, n := range []uint{0, 1, 2} {
		n := n
		t.Run(fmt.Sprintf("NewBuffer_%d", n), func(t *testing.T) {
			t.Parallel()
			underTest := ring.NewBuffer[any](n)
			assert.Zero(t, underTest.Len())
			assert.Equal(t, n, underTest.Cap())
			underTest.Reset()
			assert.Zero(t, underTest.Len())
			assert.Equal(t, n, underTest.Cap())
		})
	}
}

func TestRingWriter_PushBack(t *testing.T) {
	t.Parallel()

	underTest := ring.NewBuffer[rune](3)

	for _, test := range []struct {
		r           rune
		front, back string
	}{
		{'a', "a", ""},
		{'b', "ab", ""},
		{'c', "abc", ""},
		{'d', "bc", "d"},
		{'e', "c", "de"},
		{'f', "def", ""},
		{'g', "ef", "g"},
	} {
		ok := t.Run(string(test.r), func(t *testing.T) {
			underTest.PushBack(test.r)
			requireBuffers(t, underTest, test.front, test.back)
		})
		if !ok {
			t.FailNow()
		}
	}
}

func TestBuffer_PushAllBack(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			name, runes, front string
		}{
			{"Partial", "abc", "abc"},
			{"Full", "abcde", "abcde"},
			{"Wrapping", "abcdef", "bcdef"},
		} {
			test := test
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				underTest := ring.NewBuffer[rune](5)
				underTest.PushAllBack([]rune(test.runes))
				requireBuffers(t, underTest, test.front, "")
			})
		}
	})

	t.Run("PartiallyFilled", func(t *testing.T) {
		t.Parallel()
		for _, test := range []struct {
			name, runes, front, back string
		}{
			{"Partial", "ab", "1234ab", ""},
			{"Fill", "abc", "1234abc", ""},
			{"Wrapping", "abcde", "34abc", "de"},
			{"Full", "abcdefg", "abcdefg", ""},
			{"Overfill", "abcdefgh", "bcdefgh", ""},
		} {
			test := test
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				underTest := ring.NewBuffer[rune](7)
				underTest.PushAllBack([]rune("1234"))
				requireBuffers(t, underTest, "1234", "")

				underTest.PushAllBack([]rune(test.runes))
				requireBuffers(t, underTest, test.front, test.back)
			})
		}
	})

	t.Run("ShiftedPartiallyFilled", func(t *testing.T) {
		t.Parallel()
		for _, test := range []struct {
			name, runes, front, back string
		}{
			{"PartialFill", "ab", "4ab", ""},
			{"FillRing", "abc", "4abc", ""},
			{"WrappingPartialFill", "abcde", "4abc", "de"},
			{"WrappingFillBuf", "abcdef", "4abc", "def"},
			{"Full", "abcdefg", "abcdefg", ""},
			{"Overfill", "abcdefgh", "bcdefgh", ""},
		} {
			test := test
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				underTest := ring.NewBuffer[rune](7)
				underTest.PushAllBack([]rune("1234"))
				underTest.ShiftFront(3)
				requireBuffers(t, underTest, "4", "")

				underTest.PushAllBack([]rune(test.runes))
				requireBuffers(t, underTest, test.front, test.back)
			})
		}
	})

	t.Run("ShiftedRingFilled", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			name, runes, front, back string
		}{
			{"PartialFill", "ab", "4567", "ab"},
			{"FillBuf", "abc", "4567", "abc"},
			{"OverfillBuf", "abcde", "67", "abcde"},
			{"Full", "abcdefg", "abcdefg", ""},
			{"Overfill", "abcdefgh", "bcdefgh", ""},
		} {
			test := test
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				underTest := ring.NewBuffer[rune](7)
				underTest.PushAllBack([]rune("1234567"))
				underTest.ShiftFront(3)
				requireBuffers(t, underTest, "4567", "")

				underTest.PushAllBack([]rune(test.runes))
				requireBuffers(t, underTest, test.front, test.back)
			})
		}
	})

	t.Run("ShiftedBufferFull", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			name, runes, front, back string
		}{
			{"PartialFill", "ab", "567", "89ab"},
			{"FillBuf", "abcde", "89abcde", ""},
			{"OverfillBuf", "abcdef", "9abcde", "f"},
			{"Full", "abcdefg", "abcdefg", ""},
			{"Overfill", "abcdefgh", "bcdefgh", ""},
		} {
			test := test
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				underTest := ring.NewBuffer[rune](7)
				underTest.PushAllBack([]rune("1234"))
				underTest.ShiftFront(2)
				underTest.PushAllBack([]rune("56789"))
				requireBuffers(t, underTest, "34567", "89")

				underTest.PushAllBack([]rune(test.runes))
				requireBuffers(t, underTest, test.front, test.back)
			})
		}
	})
}

func TestBuffer_ShiftFront(t *testing.T) {
	t.Parallel()

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()

		for i := uint(0); i <= 4; i++ {
			i := i
			t.Run(strconv.FormatUint(uint64(i), 10), func(t *testing.T) {
				t.Parallel()
				underTest := ring.NewBuffer[rune](3)
				underTest.ShiftFront(i)
				requireBuffers(t, underTest, "", "")
			})
		}
	})

	t.Run("PartiallyFilled", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			shift uint
			front string
		}{{0, "ab"}, {1, "b"}, {2, ""}} {
			test := test
			t.Run(strconv.FormatUint(uint64(test.shift), 10), func(t *testing.T) {
				t.Parallel()
				underTest := ring.NewBuffer[rune](3)
				underTest.PushAllBack([]rune("ab"))
				requireBuffers(t, underTest, "ab", "")

				underTest.ShiftFront(test.shift)
				requireBuffers(t, underTest, test.front, "")
			})
		}
	})

	t.Run("Full", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			shift uint
			front string
		}{{0, "abc"}, {1, "bc"}, {2, "c"}, {3, ""}} {
			test := test
			t.Run(strconv.FormatUint(uint64(test.shift), 10), func(t *testing.T) {
				t.Parallel()
				underTest := ring.NewBuffer[rune](3)
				underTest.PushAllBack([]rune("abc"))
				requireBuffers(t, underTest, "abc", "")

				underTest.ShiftFront(test.shift)
				requireBuffers(t, underTest, test.front, "")
			})
		}
	})

	t.Run("Wrapped", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			shift       uint
			front, back string
		}{{0, "bc", "d"}, {1, "c", "d"}, {2, "d", ""}, {3, "", ""}} {
			test := test
			t.Run(strconv.FormatUint(uint64(test.shift), 10), func(t *testing.T) {
				t.Parallel()
				underTest := ring.NewBuffer[rune](3)
				underTest.PushAllBack([]rune("abc"))
				underTest.ShiftFront(1)
				underTest.PushBack('d')
				requireBuffers(t, underTest, "bc", "d")

				underTest.ShiftFront(test.shift)
				requireBuffers(t, underTest, test.front, test.back)
			})
		}
	})
}

func TestBuffer_Fuzz(t *testing.T) {
	t.Parallel()

	seed := func() uint64 {
		x := fnv.New64()
		require.NoError(t, binary.Write(x, binary.BigEndian, time.Now().UnixNano()))
		return x.Sum64()
	}()
	t.Logf("Using seed %d", seed)

	type c = int
	var counter c
	var cap int
	var expectedLen int
	var underTest *ring.Buffer[c]

	rng := rand.New(rand.NewSource(int64(seed)))

	iteration := 1
	for until := time.Now().Add(1 * time.Second); time.Now().Before(until); iteration++ {
		// Reinitialize buffer with a probability of 1%, i.e. have around 100
		// random calls to it before resetting it and starting over.
		if underTest == nil || rng.Intn(100) == 0 {
			cap = 7 + rng.Intn(10) // somewhere between 7 and 17
			underTest = ring.NewBuffer[c](uint(cap))
			counter = 1
			expectedLen = 0
		}

		// Select actions with an equal probability
		action := rng.Intn(3)
		switch action {
		case 0:
			underTest.PushBack(counter)
			counter++
			expectedLen++
			if expectedLen > cap {
				expectedLen = cap
			}

		case 1:
			size := rng.Intn(cap + 1)
			slice := make([]c, size)
			for i := range slice {
				slice[i] = counter
				counter++
			}
			underTest.PushAllBack(slice)
			expectedLen += size
			if expectedLen > cap {
				expectedLen = cap
			}

		case 2:
			n := rng.Intn(cap + 1)
			underTest.ShiftFront(uint(n))
			expectedLen -= n
			if expectedLen < 0 {
				expectedLen = 0
			}
		}

		buf, _ := underTest.ContiguousBuffer()
		bufLen := len(buf)
		assert.Equal(t, expectedLen, bufLen, "Buffer length mismatch in iteration %d after action %d", iteration, action)
		assert.Equal(t, uint(expectedLen), underTest.Len(), "Buffer length mismatch in iteration %d after action %d", iteration, action)
		checkLen := bufLen
		if bufLen > expectedLen {
			checkLen = bufLen
		}
		for pos := 0; pos < checkLen; pos++ {
			require.Equal(t, counter+pos-expectedLen, buf[pos], "In iteration %d at position %d after action %d", iteration, pos, action)
		}
		if t.Failed() {
			t.FailNow()
		}
	}

	t.Logf("Finished fuzzing after %d iterations", iteration)
}

func requireBuffers(t *testing.T, underTest *ring.Buffer[rune], front, back string) {
	t.Helper()

	require := require.New(t)

	require.Equal(uint(len(front)+len(back)), underTest.Len(), "Unexpected ring buffer length")

	frontBuf, backBuf := underTest.Buffers()

	require.NotNil(frontBuf, "Front buffers should never be nil")
	require.Equal(front, string(frontBuf), "Front buffer didn't match")
	if back == "" {
		require.Nil(backBuf, "Back buffer should have been nil")
	} else {
		require.Equal(back, string(backBuf), "Back buffer didn't match")
	}

	contiguous, shared := underTest.ContiguousBuffer()

	require.Equal(front+back, string(contiguous), "Contiguous buffer didn't match")
	if back == "" {
		require.True(shared, "Expected a shared contiguous buffer")
	} else {
		require.False(shared, "Didn't expect a shared contiguous buffer")
	}

	contiguousCopy := underTest.ContiguousCopy()
	require.NotSame(contiguous, contiguousCopy, "Contiguous copy is not a copy")
	require.Equal(front+back, string(contiguousCopy), "Contiguous copy didn't match")
}
