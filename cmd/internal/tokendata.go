// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"errors"
	"fmt"
	"os"

	"github.com/k0sproject/k0s/internal/secret"
	"github.com/k0sproject/k0s/pkg/token"
)

// EnvVarToken is the environment variable name for the join token
const EnvVarToken = "K0S_TOKEN"

// Determines the source of the join token and checks that it's the only one:
// the CLI argument, the K0S_TOKEN environment variable or the token file, in
// this order of precedence. The token itself is decoded when the returned
// getter is called, so that a token that's no longer valid, or a token file
// that's gone, is only a problem when the token is actually needed. Without
// any source, the getter yields the zero token, which fails on use.
func GetJoinTokenSource(tokenArg secret.String, tokenFile string) (func() (token.JoinToken, error), error) {
	tokenEnv := secret.Getenv(EnvVarToken).ToBytes()

	var sources int
	for _, given := range []bool{!tokenArg.IsZero(), !tokenEnv.IsZero(), tokenFile != ""} {
		if given {
			sources++
		}
	}
	if sources > 1 {
		return nil, fmt.Errorf("you can only pass one token source: either as a CLI argument, via '--token-file [path]', or via the %s environment variable", EnvVarToken)
	}

	if !tokenArg.IsZero() {
		tokenArg := tokenArg.ToBytes()
		return func() (token.JoinToken, error) {
			jt, err := token.DecodeJoinToken(tokenArg)
			if err != nil {
				return jt, fmt.Errorf("failed to decode join token argument: %w", err)
			}
			return jt, nil
		}, nil
	}

	if !tokenEnv.IsZero() {
		return func() (token.JoinToken, error) {
			jt, err := token.DecodeJoinToken(tokenEnv)
			if err != nil {
				return jt, fmt.Errorf("failed to decode join token from %s: %w", EnvVarToken, err)
			}
			return jt, nil
		}, nil
	}

	if tokenFile == "" {
		return func() (jt token.JoinToken, _ error) { return jt, nil }, nil
	}

	return func() (token.JoinToken, error) {
		var problem string
		data, err := secret.ReadFile(tokenFile)
		if errors.Is(err, os.ErrNotExist) {
			problem = "not found"
		} else if err != nil {
			return token.JoinToken{}, fmt.Errorf("failed to read token file: %w", err)
		} else if data.Len() == 0 {
			problem = "is empty"
		}
		if problem != "" {
			return token.JoinToken{}, fmt.Errorf(`token file "%s" %s`+
				`: obtain a new token via "k0s token create ..." and store it in the file`+
				` or reinstall this node via "k0s install --force ..." or "k0sctl apply --force ..."`,
				tokenFile, problem)
		}

		jt, err := token.DecodeJoinToken(data)
		if err != nil {
			return jt, fmt.Errorf("failed to decode join token from %s: %w", tokenFile, err)
		}
		return jt, nil
	}, nil
}
