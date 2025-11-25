// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"testing"
	"testing/synctest"
	"time"

	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/stretchr/testify/assert"
)

func TestIsValidLease(t *testing.T) {
	template := func() coordination.LeaseSpec {
		return coordination.LeaseSpec{
			HolderIdentity:       ptr.To(t.Name()),
			RenewTime:            ptr.To(metav1.NowMicro()),
			LeaseDurationSeconds: ptr.To[int32](3600),
		}
	}

	t.Run("NilValuesAreInvalid", func(t *testing.T) {
		assert.True(t, IsValidLease(ptr.To(template())))

		nilHodler := template()
		nilHodler.HolderIdentity = nil
		assert.False(t, IsValidLease(&nilHodler))

		emptyHodler := template()
		emptyHodler.HolderIdentity = ptr.To("")
		assert.False(t, IsValidLease(&emptyHodler))

		nilRenewTime := template()
		nilRenewTime.RenewTime = nil
		assert.False(t, IsValidLease(&nilRenewTime))

		nilLeaseDurationSecs := template()
		nilLeaseDurationSecs.LeaseDurationSeconds = nil
		assert.False(t, IsValidLease(&nilLeaseDurationSecs))
	})

	t.Run("Expires", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			leaseSpec := template()

			assert.True(t, IsValidLease(&leaseSpec))
			time.Sleep(3600*time.Second - time.Nanosecond)
			assert.True(t, IsValidLease(&leaseSpec))
			time.Sleep(time.Nanosecond)
			assert.False(t, IsValidLease(&leaseSpec))
		})
	})
}
