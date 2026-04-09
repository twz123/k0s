//go:build !linux || arm || riscv64

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nllb

// Indicates if Envoy is supported on this platform.
const EnvoySupported = false
