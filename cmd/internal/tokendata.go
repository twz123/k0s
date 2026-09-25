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

// CheckSingleTokenSource verifies that at most one token source is provided.
// Returns an error if multiple sources are specified.
func CheckSingleTokenSource(tokenArg secret.String, tokenFile string) error {
	tokenSources := 0
	if !tokenArg.IsZero() {
		tokenSources++
	}
	if tokenFile != "" {
		tokenSources++
	}
	if !secret.Getenv(EnvVarToken).IsZero() {
		tokenSources++
	}

	if tokenSources > 1 {
		return fmt.Errorf("you can only pass one token source: either as a CLI argument, via '--token-file [path]', or via the %s environment variable", EnvVarToken)
	}

	return nil
}

// Resolves the join token from multiple possible sources, in this order of
// precedence:
//
//   - CLI argument,
//   - K0S_TOKEN environment variable,
//   - or token file.
//
// Returns the zero token if no token source is available.
func GetJoinToken(tokenArg secret.String, tokenFile string) (jt token.JoinToken, err error) {
	if jt, err = token.DecodeJoinToken(tokenArg.ToBytes()); err == nil {
		return
	} else if !errors.Is(err, secret.NoBytesError{}) {
		return jt, fmt.Errorf("failed to decode join token argument: %w", err)
	}

	if jt, err = token.DecodeJoinToken(secret.Getenv(EnvVarToken).ToBytes()); err == nil {
		return
	} else if !errors.Is(err, secret.NoBytesError{}) {
		return jt, fmt.Errorf("failed to decode join token from %s: %w", EnvVarToken, err)
	}

	if tokenFile == "" {
		return jt, nil
	}

	var problem string
	data, err := secret.ReadFile(tokenFile)
	if errors.Is(err, os.ErrNotExist) {
		problem = "not found"
	} else if err != nil {
		return jt, fmt.Errorf("failed to read token file: %w", err)
	} else if data.Len() == 0 {
		problem = "is empty"
	}
	if problem != "" {
		return jt, fmt.Errorf(`token file "%s" %s`+
			`: obtain a new token via "k0s token create ..." and store it in the file`+
			` or reinstall this node via "k0s install --force ..." or "k0sctl apply --force ..."`,
			tokenFile, problem)
	}

	jt, err = token.DecodeJoinToken(data)
	if err != nil {
		err = fmt.Errorf("failed to decode join token from %s: %w", tokenFile, err)
	}
	return
}
