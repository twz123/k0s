package io

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Unit tests for ThroughputWriter
func TestThroughputWriter(t *testing.T) {
	var (
		elapsed            WindowerFunc
		timesElapsedCalled uint
		numElapsedWindows  uint64
	)
	elapsed = WindowerFunc(func() (uint64, TimeWindow) {
		timesElapsedCalled++
		return numElapsedWindows, elapsed
	})

	var (
		timesUpdateThroughputCalled uint
		lastByteCount               uint64
		lastWindowCount             uint
		updateThroughputErr         error
	)
	updateThroughput := func(numBytes uint64, numWindows uint) error {
		timesUpdateThroughputCalled++
		lastByteCount, lastWindowCount = numBytes, numWindows
		return updateThroughputErr
	}

	underTest := NewThroughputWriter(4, elapsed, updateThroughput)

	// Write ten bytes in first window
	numElapsedWindows = 0
	n, err := underTest.Write([]byte("1234567890"))
	assert.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, uint(1), timesElapsedCalled)
	assert.Equal(t, uint(1), timesUpdateThroughputCalled)
	assert.Equal(t, uint64(10), lastByteCount)
	assert.Equal(t, uint(1), lastWindowCount)

	// Write six bytes in second window
	numElapsedWindows = 1
	n, err = underTest.Write([]byte("123456"))
	assert.NoError(t, err)
	assert.Equal(t, 6, n)
	assert.Equal(t, uint(2), timesElapsedCalled)
	assert.Equal(t, uint(2), timesUpdateThroughputCalled)
	assert.Equal(t, uint64(16), lastByteCount)
	assert.Equal(t, uint(2), lastWindowCount)

	// Write four bytes in fourth window
	numElapsedWindows = 2
	n, err = underTest.Write([]byte("1234"))
	assert.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, uint(3), timesElapsedCalled)
	assert.Equal(t, uint(3), timesUpdateThroughputCalled)
	assert.Equal(t, uint64(20), lastByteCount)
	assert.Equal(t, uint(4), lastWindowCount)

	// Write eight bytes in eighth window, then error out
	numElapsedWindows, updateThroughputErr = 4, assert.AnError
	n, err = underTest.Write([]byte("12345678"))
	assert.Equal(t, 0, n)
	assert.Same(t, assert.AnError, err)
	assert.Equal(t, uint(4), timesElapsedCalled)
	assert.Equal(t, uint(4), timesUpdateThroughputCalled)
	assert.Equal(t, uint64(8), lastByteCount)
	assert.Equal(t, uint(4), lastWindowCount)
}
