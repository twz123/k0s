//go:build windows

// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package dism

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Specifies the state of a package or a feature.
//
// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/dismpackagefeaturestate-enumeration
type PackageFeatureState int32

const (
	// The package or feature is not present.
	StateNotPresent PackageFeatureState = iota

	// An uninstall process for the package or feature is pending. Additional
	// processes are pending and must be completed before the package or feature
	// is successfully uninstalled.
	StateUninstallPending

	// The package or feature is staged.
	StateStaged

	// Metadata about the package or feature has been added to the system, but
	// the package or feature is not present.
	StateRemoved

	// The package or feature is installed.
	StateInstalled

	// The install process for the package or feature is pending. Additional
	// processes are pending and must be completed before the package or feature
	// is successfully installed.
	StateInstallPending

	// The package or feature has been superseded by a more recent package or
	// feature.
	StateSuperseded

	// The package or feature is partially installed. Some parts of the package
	// or feature have not been installed.
	StatePartiallyInstalled
)

// Specifies whether a restart is required after enabling a feature or installing a package.
//
// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/dismrestarttype-enumeration
type RestartType int32

const (
	// No restart is required.
	DismRestartNo RestartType = iota
	// This package or feature might require a restart.
	DismRestartPossible
	// This package or feature always requires a restart.
	DismRestartRequired
)

// Describes advanced feature information, such as installed state and whether a
// restart is required after installation.
type FeatureInfo struct {
	Name             string
	State            PackageFeatureState
	DisplayName      string
	Description      string
	RestartRequired  RestartType
	CustomProperties []CustomProperty
}

type CustomProperty struct {
	Name  string
	Value string
	Path  string
}

func (s *Session) GetFeatureInfo(name string) (_ *FeatureInfo, err error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.refCounter == nil {
		return nil, alreadyClosed()
	}

	// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/dismfeatureinfo-structure
	type featureInfo struct {
		/* PCWSTR                  */ FeatureName *uint16
		/* DismPackageFeatureState */ FeatureState PackageFeatureState
		/* PCWSTR                  */ DisplayNameLo, DisplayNameHi uint32
		/* PCWSTR                  */ DescriptionLo, DescriptionHi uint32
		/* DismRestartType         */ RestartRequired RestartType
		/* DismCustomProperty*     */ CustomProperty *struct {
			/* PCWSTR */ Name *uint16
			/* PCWSTR */ Value *uint16
			/* PCWSTR */ Path *uint16
		}
		/* UINT                    */ CustomPropertyCount uint32
	}

	var out *featureInfo

	// https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/dism/dismgetfeatureinfo-function
	r, _, _ := /* HRESULT */ modDISMAPI.NewProc("DismGetFeatureInfo").Call(
		/* _In_     DismSession           Session           */ uintptr(s.session),
		/* _In_     PCWSTR                FeatureName       */ uintptr(unsafe.Pointer(namePtr)),
		/* _In_opt_ PCWSTR                Identifier        */ 0,
		/* _In_opt_ DismPackageIdentifier PackageIdentifier */ 0,
		/* _Out_    DismFeatureInfo       **FeatureInfo     */ uintptr(unsafe.Pointer(&out)),
	)
	if err := hresult("DismGetFeatureInfo", uint32(r)); err != nil {
		// Returns HRESULT_FROM_WIN32(ERROR_ALREADY_EXISTS) if the DismSession already has an image associated with it.
		// Returns a Win32 error code mapped to an HRESULT for other errors.
		return nil, err
	}
	defer func() { err = errors.Join(err, dismDelete(unsafe.Pointer(out))) }()

	props := make([]CustomProperty, out.CustomPropertyCount)
	for i, prop := range unsafe.Slice(out.CustomProperty, out.CustomPropertyCount) {
		props[i] = CustomProperty{
			Name:  windows.UTF16PtrToString(prop.Name),
			Value: windows.UTF16PtrToString(prop.Value),
			Path:  windows.UTF16PtrToString(prop.Path),
		}
	}

	ret := FeatureInfo{
		Name:  windows.UTF16PtrToString(out.FeatureName),
		State: out.FeatureState,
		DisplayName: windows.UTF16PtrToString(
			(*uint16)(unsafe.Pointer(uintptr(out.DisplayNameLo) | uintptr(out.DisplayNameHi)<<32)),
		),
		Description: windows.UTF16PtrToString(
			(*uint16)(unsafe.Pointer(uintptr(out.DescriptionLo) | uintptr(out.DescriptionHi)<<32)),
		),
		RestartRequired:  out.RestartRequired,
		CustomProperties: props,
	}

	return &ret, nil
}
