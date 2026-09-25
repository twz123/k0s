// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package token

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"

	"github.com/k0sproject/k0s/internal/secret"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// DecodeJoinToken decodes a join token from its encoded form, see
// [JoinToken.RevealEncoded].
func DecodeJoinToken(encoded string) (JoinToken, error) {
	gzData, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return JoinToken{}, err
	}

	gz, err := gzip.NewReader(bytes.NewBuffer(gzData))
	if err != nil {
		return JoinToken{}, err
	}

	var buf bytes.Buffer
	_, err = io.Copy(&buf, gz)
	closeErr := gz.Close()
	if err != nil {
		return JoinToken{}, err
	}
	if closeErr != nil {
		return JoinToken{}, closeErr
	}

	return JoinToken{secret.From[JoinToken](buf.Bytes())}, nil
}

func GetTokenType(clientCfg *clientcmdapi.Config) string {
	for _, kubeContext := range clientCfg.Contexts {
		return kubeContext.AuthInfo
	}

	return ""
}
