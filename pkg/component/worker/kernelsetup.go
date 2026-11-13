//go:build !linux

// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package worker

import "context"

// KernelSetup comment
func KernelSetup(context.Context) {}
