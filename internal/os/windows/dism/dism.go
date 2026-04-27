//go:build windows

// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

// Deployment Image Servicing and Management (DISM) API
//
// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/deployment-image-servicing-and-management--dism--api
package dism

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modDISMAPI = windows.NewLazySystemDLL("dismapi.dll")
)

type API struct {
	mu           sync.RWMutex
	close        func() error
	openSessions atomic.Uint32
}

var dsim = struct {
	mu         sync.Mutex
	refCounter uint
}{}

func Initialize() (*API, error) {
	dsim := &dsim

	dsim.mu.Lock()
	defer dsim.mu.Unlock()

	if dsim.refCounter == 0 {
		// Initializes the DISM API. DismInitialize must be called once per process,
		// before calling any other DISM API functions.
		//
		// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/disminitialize-function
		r, _, _ := /* HRESULT */ modDISMAPI.NewProc("DismInitialize").Call(
			/* _In_     DismLogLevel LogLevel         */ 0, // DismLogErrors
			/* _In_opt_ PCWSTR       LogFilePath      */ 0, // NULL
			/* _In_opt_ PCWSTR       ScratchDirectory */ 0, // NULL
		)
		if err := hresult("DismInitialize", uint32(r)); err != nil {
			return nil, err
		}
	}

	dsim.refCounter++
	return &API{close: func() error {
		dsim.mu.Lock()
		defer dsim.mu.Unlock()

		switch dsim.refCounter {
		case 0:
			return errors.New("not initialized")

		case 1:
			// Shuts down DISM API. DismShutdown must be called once per process.
			// Other DISM API function calls will fail after DismShutdown has been
			// called.
			//
			// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/dismshutdown-function
			r, _, _ := /* HRESULT */ modDISMAPI.NewProc("DismShutdown").Call( /* VOID */ )
			if err := hresult("DismShutdown", uint32(r)); err != nil {
				// Returns DISMAPI_E_DISMAPI_NOT_INITIALIZED if DismInitialize has not been called.
				// Returns DISMAPI_E_OPEN_SESSION_HANDLES if any open DismSession have not been closed.
				return err
			}
			fallthrough

		default:
			dsim.refCounter--
		}

		return nil

	}}, nil
}

func (a *API) Shutdown() error {
	a.mu.Lock()
	close := a.close
	switch {
	case a.openSessions.Load() != 0:
		close = stillOpenSessions
	case close == nil:
		close = alreadyClosed
	default:
		a.close = nil
	}
	a.mu.Unlock()

	return close()
}

// Opens a Session with the online Windows installation.
func (a *API) OpenOnlineSession() (_ *Session, err error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.close == nil {
		return nil, alreadyClosed()
	}

	a.openSessions.Add(1)
	defer func() {
		if err != nil {
			a.openSessions.Add(math.MaxUint32) // Decrement by 1 🙄
		}
	}()

	// Indicates to DismOpenSession that the online operating system, %windir%,
	// should be associated to the DismSession for servicing.
	//
	// Windows Assessment and Deployment Kit (Windows ADK)
	// Feature: Deployment Tools
	// File: C:\Program Files (x86)\Windows Kits\10\Assessment and Deployment Kit\Deployment Tools\SDKs\DismApi\Include\dismapi.h
	//
	// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/dism-api-constants
	const dismOnlineImage = "DISM_{53BFAE52-B167-4E2F-A258-0A37B57FF845}"

	imagePathPtr, _ := windows.UTF16PtrFromString(dismOnlineImage)
	var session uint

	// Associates an offline or online Windows image with a DISMSession.
	//
	// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/dismopensession-function
	r, _, err := /* HRESULT */ modDISMAPI.NewProc("DismOpenSession").Call(
		/* _In_     PCWSTR      ImagePath        */ uintptr(unsafe.Pointer(imagePathPtr)),
		/* _In_opt_ PCWSTR      WindowsDirectory */ 0,
		/* _In_opt_ PCWSTR      SystemDrive      */ 0,
		/* _Out_    DismSession *Session         */ uintptr(unsafe.Pointer(&session)),
	)
	if err := hresult("DismOpenSession", uint32(r)); err != nil {
		// Returns HRESULT_FROM_WIN32(ERROR_ALREADY_EXISTS) if the DismSession already has an image associated with it.
		// Returns a Win32 error code mapped to an HRESULT for other errors.
		return nil, err
	}

	return &Session{session: session, refCounter: &a.openSessions}, nil
}

type Session struct {
	mu         sync.RWMutex
	session    uint
	refCounter *atomic.Uint32
	closedErr  error
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refCounter != nil {
		// Associates an offline or online Windows image with a DISMSession.
		//
		// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/dismclosesession-function
		r, _, _ := /* HRESULT */ modDISMAPI.NewProc("DismCloseSession").Call(
			/* _In_ DismSession Session */ uintptr(s.session),
		)
		s.closedErr = hresult("DismCloseSession", uint32(r))
		s.refCounter.Add(math.MaxUint32) // Decrement by 1 🙄
		s.refCounter = nil
	}

	return s.closedErr
}

// Releases resources held by a structure or an array of structures returned by
// other DISM API Functions.
//
// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/dismdelete-function
func dismDelete(ptr unsafe.Pointer) error {
	r, _, _ := /* HRESULT */ modDISMAPI.NewProc("DismDelete").Call(
		/* _In_ VOID *DismStructure */ uintptr(ptr),
	)
	return hresult("DismDelete", uint32(r))
}

func hresult(syscall string, hresult uint32) error {
	if hresult == 0 {
		return nil
	}
	return os.NewSyscallError(syscall, hresultError(hresult))
}

func alreadyClosed() error     { return errors.New("already closed") }
func stillOpenSessions() error { return errors.New("there are still open sessions") }

type DismSession uintptr

type hresultError uint32

func (e hresultError) Error() string {
	return fmt.Sprintf("HRESULT 0x%08x", uint32(e))
}
