//go:build !linux

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package applier

func handleCreateWatchErr(err error) (error, bool) { return err, false }
func handleAddWatchErr(err error) (error, bool)    { return err, false }
