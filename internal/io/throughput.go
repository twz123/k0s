package io

import (
	"math"
	"time"
)

// Allows for measuring the elapse of time windows.
type TimeWindow interface {
	// Returns the current number of time windows that have elapsed since this
	// time window. The returned Windower represents the time window of the
	// current point in time for subsequent measurements.
	Elapsed() (uint64, TimeWindow)
}

// A function that satisfies the [TimeWindow] interface.
type WindowerFunc func() (uint64, TimeWindow)

var _ TimeWindow = (WindowerFunc)(nil)

// Elapsed implements [TimeWindow].
func (f WindowerFunc) Elapsed() (uint64, TimeWindow) {
	return f()
}

// The duration for a sliding window.
type Sliding time.Duration

var _ TimeWindow = Sliding(0)

// Returns a new SlidingWindow starting at the given time.
func (s Sliding) StartAt(start time.Time) *SlidingWindow {
	return &SlidingWindow{start, s}
}

// Returns a new SlidingWindow starting at the current time.
func (s Sliding) StartNow() *SlidingWindow {
	return s.StartAt(time.Now())
}

// Elapsed implements [TimeWindow]. Returns zero elapsed windows and a new
// SlidingWindow starting at the current time.
func (s Sliding) Elapsed() (uint64, TimeWindow) {
	return 0, s.StartNow()
}

// SlidingWindow represents a time window by using a start time and a duration.
type SlidingWindow struct {
	Start    time.Time
	Duration Sliding
}

// Returns a new SlidingWindow whose start time is advanced by n time windows.
func (s *SlidingWindow) AdvancedBy(n uint64) *SlidingWindow {
	return s.Duration.StartAt(s.Start.Add(time.Duration(n) * time.Duration(s.Duration)))
}

// Calculates the number of elapsed time windows until the given time along with
// the corresponding [SlidingWindow] for until.
func (s *SlidingWindow) ElapsedUntil(until time.Time) (uint64, *SlidingWindow) {
	elapsedWindows := until.Sub(s.Start) / time.Duration(s.Duration)
	if elapsedWindows < 1 {
		return 0, s
	}

	return uint64(elapsedWindows), s.AdvancedBy(uint64(elapsedWindows))
}

// Elapsed implements [TimeWindow]. Calculates the number of elapsed time
// windows until the given time along with the current [SlidingWindow].
func (s *SlidingWindow) Elapsed() (uint64, TimeWindow) {
	return s.ElapsedUntil(time.Now())
}

// An [io.Writer] that measures the write throughput over the given number of
// time windows.
type ThroughputWriter struct {
	lastWrite       TimeWindow
	lastWriteWindow uint
	windows         []counter

	updateThroughput func(uint64, uint) error

	sum            counter
	elapsedWindows counter
}

// Returns an [io.Writer] that measures the write throughput over the given
// number of time windows, starting the time window measurement with the given
// [TimeWindow] on the first call to Write. The updateThroughput callback function
// receives the total number of bytes written over the given number of windows,
// where the number of windows is between 1 and numWindows. It is called on each
// call to Write. If it returns an error, that error is returned verbatim by
// Write.
func NewThroughputWriter(numWindows uint, start TimeWindow, updateThroughput func(numBytes uint64, numWindows uint) error) *ThroughputWriter {
	return &ThroughputWriter{
		lastWrite:        start,
		windows:          make([]counter, numWindows),
		updateThroughput: updateThroughput,
	}
}

// Write implements [io.Writer].
func (tw *ThroughputWriter) Write(p []byte) (int, error) {
	var elapsed uint64
	elapsed, tw.lastWrite = tw.lastWrite.Elapsed()
	numWindows := uint(len(tw.windows))

	for elapsed := uint(min(elapsed, uint64(numWindows))); elapsed > 0; elapsed-- {
		tw.lastWriteWindow = (tw.lastWriteWindow + 1) % numWindows
		tw.sum.Sub(uint64(tw.windows[tw.lastWriteWindow]))
		tw.windows[tw.lastWriteWindow] = 0
	}

	writtenBytes := uint64(len(p))
	tw.windows[tw.lastWriteWindow].Add(writtenBytes)
	tw.elapsedWindows.Add(elapsed)
	tw.sum.Add(writtenBytes)

	if uint64(tw.elapsedWindows) < uint64(numWindows) {
		numWindows = uint(tw.elapsedWindows) + 1
	}

	// FIXME probably also include the current window's bytes, as it's not complete yet.
	if err := tw.updateThroughput(uint64(tw.sum), numWindows); err != nil {
		return 0, err
	}

	return len(p), nil
}

type counter uint64

// Saturating addition.
func (c *counter) Add(summand uint64) {
	if sum := uint64(*c) + summand; sum < uint64(*c) {
		*c = math.MaxUint64
	} else {
		*c = counter(sum)
	}
}

// Saturating subtraction.
func (c *counter) Sub(subtrahend uint64) {
	if diff := uint64(*c) - subtrahend; diff > uint64(*c) {
		*c = 0
	} else {
		*c = counter(diff)
	}
}
