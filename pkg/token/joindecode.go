// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package token

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"io"

	"github.com/k0sproject/k0s/internal/secret"
)

// DecodeJoinToken decodes a join token from its encoded form, see
// [JoinToken.Encode].
func DecodeJoinToken(encoded secret.Bytes) (JoinToken, error) {
	return encoded.Use(func(data []byte) (jt JoinToken, err error) {
		if data, err = base64.StdEncoding.AppendDecode(nil, data); err != nil {
			return
		}

		gz, err := gzip.NewReader(bytes.NewBuffer(data))
		if err != nil {
			return
		}
		data, err = io.ReadAll(gz)
		if err = errors.Join(err, gz.Close()); err != nil {
			return
		}

		jt.kubeconfig.Store(data)
		return
	})
}

// Compresses and base64 encodes the token into the given writer. It fails for
// the zero token, which holds nothing to encode, and for write errors.
func (t JoinToken) Encode(w io.Writer) error {
	kubeconfig, err := t.kubeconfig.Reveal()
	if err != nil {
		return err
	}

	enc := base64.NewEncoder(base64.StdEncoding, w)
	gz, err := gzip.NewWriterLevel(enc, gzip.BestCompression)
	if err != nil {
		return err
	}

	_, err = gz.Write(kubeconfig)
	return errors.Join(err, gz.Close(), enc.Close())
}
