// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// Describes which IP address families a cluster uses, and which one of them is
// the primary family.
//
// The zero value is [SingleStackIPv4].
type IPStack uint8

const (
	SingleStackIPv4 IPStack = iota // An IPv4-only cluster.
	SingleStackIPv6                // An IPv6-only cluster.
	DualStackIPv4                  // A dual-stack cluster whose primary family is IPv4.
	DualStackIPv6                  // A dual-stack cluster whose primary family is IPv6.
)

// Combines the primary address family and dual-stack flag into an IPStack.
// Any primary IP family other than IPv6 is treated as IPv4.
func IPStackFrom(primaryIPFamily corev1.IPFamily, dualStack bool) IPStack {
	if primaryIPFamily == corev1.IPv6Protocol {
		if dualStack {
			return DualStackIPv6
		}
		return SingleStackIPv6
	}

	if dualStack {
		return DualStackIPv4
	}
	return SingleStackIPv4
}

// Returns the enabled IP families in the comma-separated, primary-first
// notation that Kubernetes uses for IP families and CIDRs.
func (s IPStack) String() string {
	switch s {
	case SingleStackIPv4:
		return string(corev1.IPv4Protocol)
	case SingleStackIPv6:
		return string(corev1.IPv6Protocol)
	case DualStackIPv4:
		return string(corev1.IPv4Protocol) + "," + string(corev1.IPv6Protocol)
	case DualStackIPv6:
		return string(corev1.IPv6Protocol) + "," + string(corev1.IPv4Protocol)
	default:
		return fmt.Sprintf("IPStack(%d)", uint8(s))
	}
}

// Returns the enabled IP families, primary family first.
// Returns nil for invalid values.
func (s IPStack) IPFamilies() []corev1.IPFamily {
	switch s {
	case SingleStackIPv4:
		return []corev1.IPFamily{corev1.IPv4Protocol}
	case SingleStackIPv6:
		return []corev1.IPFamily{corev1.IPv6Protocol}
	case DualStackIPv4:
		return []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}
	case DualStackIPv6:
		return []corev1.IPFamily{corev1.IPv6Protocol, corev1.IPv4Protocol}
	default:
		return nil
	}
}

// Returns the primary IP family.
// Returns [corev1.IPFamilyUnknown] for invalid values.
func (s IPStack) PrimaryIPFamily() corev1.IPFamily {
	switch s {
	case SingleStackIPv4, DualStackIPv4:
		return corev1.IPv4Protocol
	case SingleStackIPv6, DualStackIPv6:
		return corev1.IPv6Protocol
	default:
		return corev1.IPFamilyUnknown
	}
}

// Returns the secondary IP family of a dual-stack cluster.
// Returns [corev1.IPFamilyUnknown] for single-stack and invalid values.
func (s IPStack) SecondaryIPFamily() corev1.IPFamily {
	switch s {
	case DualStackIPv4:
		return corev1.IPv6Protocol
	case DualStackIPv6:
		return corev1.IPv4Protocol
	default:
		return corev1.IPFamilyUnknown
	}
}

// Indicates whether IPv4 is one of the enabled IP families.
func (s IPStack) IsIPv4Enabled() bool {
	return s == SingleStackIPv4 || s.IsDualStack()
}

// Indicates whether IPv6 is one of the enabled IP families.
func (s IPStack) IsIPv6Enabled() bool {
	return s == SingleStackIPv6 || s.IsDualStack()
}

// IsDualStack indicates whether both IP families are enabled.
func (s IPStack) IsDualStack() bool {
	switch s {
	case DualStackIPv4, DualStackIPv6:
		return true
	default:
		return false
	}
}
