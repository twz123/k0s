// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package token

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"

	"github.com/k0sproject/k0s/internal/secret"
)

// JoinToken is a kubeconfig with bootstrap credentials for the join API. It's
// secret, since it grants access to the cluster. It travels as a single
// string, compressed and base64 encoded, see [JoinToken.RevealEncoded] and
// [DecodeJoinToken].
type JoinToken struct {
	secret.Value[JoinToken, []byte]
}

// Reveals the token in its encoded form: the kubeconfig, compressed and base64
// encoded, so that it can be handed over as a single string. It fails for the
// zero token, which holds no kubeconfig to encode.
func (t JoinToken) RevealEncoded() (string, error) {
	kubeconfig, err := t.Reveal()
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := gz.Write(kubeconfig); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
