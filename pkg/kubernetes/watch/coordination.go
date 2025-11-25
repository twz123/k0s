// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	coordinationv1 "k8s.io/api/coordination/v1"
)

func Leases(client Provider[*coordinationv1.LeaseList]) *Watcher[coordinationv1.Lease] {
	return FromClient[*coordinationv1.LeaseList, coordinationv1.Lease](client)
}
