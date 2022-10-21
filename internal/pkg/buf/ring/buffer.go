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

// Buffer is a ring buffer that retains a fixed internal buffer, discarding the
// oldest items whenever new ones get pushed that would otherwise exceed the
// buffer's capacity.
type Buffer[T any] struct {
	ring                    []T
	notEmpty                bool
	nextReadAt, nextWriteAt uint
}

func NewBuffer[T any](capacity uint) *Buffer[T] {
	return &Buffer[T]{ring: make([]T, capacity)}
}

// Reset resets the buffer to its initial empty state. The internal ring is not
// zeroed, so any memory referenced by it won't be reclaimed by garbage
// collection.
func (b *Buffer[T]) Reset() {
	if b == nil {
		return
	}
	b.reset()
}

func (b *Buffer[T]) reset() {
	*b = Buffer[T]{ring: b.ring}
}

// PushBack pushes the new item into the buffer, possibly overwriting the oldest
// item (according to the order in which items were pushed) if the buffer is
// full.
func (b *Buffer[T]) PushBack(item T) {
	full := b.notEmpty && b.nextReadAt == b.nextWriteAt
	b.ring[b.nextWriteAt] = item
	b.nextWriteAt = (b.nextWriteAt + 1) % uint(len(b.ring))
	if full {
		b.nextReadAt = b.nextWriteAt
	}
	b.notEmpty = true
}

// PushAllBack pushes the new items into the buffer, possibly overwriting the
// oldest items (according to the order in which items were pushed) if the
// buffer becomes full.
func (b *Buffer[T]) PushAllBack(items []T) {
	ln := uint(len(items))
	if ln == 0 {
		return
	}

	// Optimization when the whole ring can be replaced
	cp := uint(len(b.ring))
	if ln >= cp {
		copy(b.ring, items[ln-cp:])
		b.nextWriteAt = 0
		b.nextReadAt = 0
		b.notEmpty = true
		return
	}

	copied := uint(copy(b.ring[b.nextWriteAt:], items))
	var wrapped bool
	if copied < ln {
		copied += uint(copy(b.ring, items[copied:]))
		wrapped = true
	}

	nextWriteAt := b.nextWriteAt + copied
	var adjustRead bool

	if b.nextWriteAt == b.nextReadAt {
		if b.notEmpty { // buffer full
			adjustRead = true
		}
	} else if b.nextWriteAt < b.nextReadAt {
		if nextWriteAt > b.nextReadAt {
			// items have been discarded, adjust accordingly
			adjustRead = true
		}
	} else if b.nextWriteAt > b.nextReadAt {
		if wrapped && (nextWriteAt%cp) > b.nextReadAt {
			// items have been discarded, adjust accordingly
			adjustRead = true
		}
	}

	nextWriteAt %= cp
	b.nextWriteAt = nextWriteAt
	if adjustRead {
		b.nextReadAt = nextWriteAt
	}
	b.notEmpty = true
}

// ShiftFront discards the oldest n items (according to the order in which items
// were pushed). The internal ring is not zeroed, so any memory referenced by
// the discarded items won't be reclaimed by garbage collection until
// overwritten by subsequent pushes.
func (b *Buffer[T]) ShiftFront(n uint) {
	ln := n
	if ln == 0 {
		return
	}

	cp := uint(len(b.ring))
	if n >= cp {
		b.reset()
		return
	}

	nextReadAt := b.nextReadAt + n

	if b.nextReadAt == b.nextWriteAt {
		if !b.notEmpty { // buffer was empty already
			return
		}
	} else if b.nextReadAt < b.nextWriteAt {
		if nextReadAt >= b.nextWriteAt { // buffer is empty now
			b.reset()
			return
		}
	} else if b.nextReadAt > b.nextWriteAt {
		wrappedNextReadAt := (nextReadAt % cp)
		if (wrappedNextReadAt < nextReadAt) && wrappedNextReadAt >= b.nextWriteAt {
			// buffer is empty now
			b.reset()
			return
		}
	}

	nextReadAt %= cp
	b.nextReadAt = nextReadAt
}

// Cap returns the capacity of the buffer's ring, that is, the total space
// allocated for the buffer's data. Cap is nil safe.
func (b *Buffer[T]) Cap() uint {
	if b == nil {
		return 0
	}
	return uint(len(b.ring))
}

// Len returns the number of items that have been pushed to this buffer. Len is
// nil safe. The following invariant applies:
//
//	b.Len() == uint(len(b.ContiguousCopy()))
func (b *Buffer[T]) Len() uint {
	if b == nil {
		return 0
	}

	if b.nextReadAt == b.nextWriteAt {
		if b.notEmpty { // buffer is full
			return uint(len(b.ring))
		}

		return 0 // buffer is empty
	}

	if b.nextReadAt < b.nextWriteAt {
		return b.nextWriteAt - b.nextReadAt
	}

	// invariant: b.nextReadAt > b.nextWriteAt
	return (uint(len(b.ring)) + b.nextWriteAt) - b.nextReadAt
}

func (b *Buffer[T]) Buffers() (front, back []T) {
	if b.nextReadAt >= b.nextWriteAt && b.notEmpty {
		front = b.ring[b.nextReadAt:len(b.ring)]
		if b.nextWriteAt != 0 {
			back = b.ring[0:b.nextWriteAt]
		}
	} else {
		front = b.ring[b.nextReadAt:b.nextWriteAt]
	}

	return
}

func (b *Buffer[T]) ContiguousBuffer() (_ []T, shared bool) {
	front, back := b.Buffers()
	if back == nil {
		return front, true
	}

	return append(
		append(
			make([]T, 0, len(b.ring)),
			front...,
		),
		back...,
	), false
}

func (b *Buffer[T]) ContiguousCopy() []T {
	contiguous, shared := b.ContiguousBuffer()
	if shared {
		return append([]T(nil), contiguous...)
	}
	return contiguous
}

func (b *Buffer[T]) ForEach(f func(T)) {
	b.ForEachUntil(func(t T) bool { f(t); return true })
}

func (b *Buffer[T]) ForEachUntil(f func(T) bool) {
	front, back := b.Buffers()
	for _, item := range front {
		if !f(item) {
			return
		}
	}
	for _, item := range back {
		if !f(item) {
			return
		}
	}
}
