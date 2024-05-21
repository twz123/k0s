//go:build linux

/*
Copyright 2024 k0s authors

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

package linux

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Sends a signal to the process specified by conn.
//
// The calling process must either be in the same PID namespace as the process
// referred to by conn, or be in an ancestor of that namespace.
//
// Signal allows the avoidance of race conditions that occur when using
// traditional interfaces (such as [os.Signal] or [syscall.Kill]) to signal a
// process. The problem is that the traditional interfaces specify the target
// process via the PID, with the result that the sender may accidentally send a
// signal to the wrong process if the originally intended target process has
// terminated and its PID has been recycled for another process. By contrast, a
// PID file descriptor is a stable reference to a specific process; if that
// process terminates, Signal fails with [syscall.ESRCH].
//
// The conn needs to provide a PID file descriptor, a file descriptor that
// refers to process. Such a file descriptor can be obtained in any of the
// following ways:
//   - by opening a /proc/pid directory;
//   - using pidfd_open(2); or
//   - via the PID file descriptor that is returned by a call to clone(2) or
//     clone3(2) that specifies the CLONE_PIDFD flag.
//
// Since Linux 5.1.
//
// https://man7.org/linux/man-pages/man2/pidfd_send_signal.2.html
// https://github.com/torvalds/linux/commit/3eb39f47934f9d5a3027fe00d906a45fe3a15fad
func Signal(conn syscall.Conn, signal os.Signal) error {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return err
	}

	outerErr := rawConn.Control(func(fd uintptr) {
		sig, ok := signal.(syscall.Signal)
		if !ok {
			err = fmt.Errorf("%w: %s", errors.ErrUnsupported, signal)
			return
		}

		// If the info argument is a NULL pointer, this is equivalent to
		// specifying a pointer to a siginfo_t buffer whose fields match the
		// values that are implicitly supplied when a signal is sent using
		// kill(2):
		//
		//   * si_signo is set to the signal number;
		//   * si_errno is set to 0;
		//   * si_code is set to SI_USER;
		//   * si_pid is set to the caller's PID; and
		//   * si_uid is set to the caller's real user ID.
		info := (*unix.Siginfo)(nil)

		// The flags argument is reserved for future use; currently, this
		// argument must be specified as 0.
		flags := 0

		err = os.NewSyscallError("pidfd_send_signal", unix.PidfdSendSignal(int(fd), sig, info, flags))
	})
	if outerErr != nil {
		return outerErr
	}

	return err
}
