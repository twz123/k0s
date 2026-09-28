// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKubeconfigGetterFromJoinToken(t *testing.T) {
	t.Run("no getter without a token source", func(t *testing.T) {
		assert.Nil(t, kubeconfigGetterFromJoinToken(nil))
	})

	t.Run("propagates token loading errors", func(t *testing.T) {
		loadErr := errors.New("load failed")
		getter := kubeconfigGetterFromJoinToken(func() (string, error) { return "", loadErr })
		require.NotNil(t, getter)

		_, err := getter()
		assert.Equal(t, loadErr, err)
	})

	t.Run("rejects undecodable tokens", func(t *testing.T) {
		getter := kubeconfigGetterFromJoinToken(func() (string, error) { return "not-base64", nil })
		require.NotNil(t, getter)

		_, err := getter()
		assert.ErrorContains(t, err, "failed to decode join token")
	})
}
