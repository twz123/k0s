//go:build !windows

package log

import (
	"errors"
	"os"
)

func SwapBufferedOutput(func() (*os.File, error)) error { return errors.ErrUnsupported }

func initBuffer()     {}
func shutdownBuffer() {}
