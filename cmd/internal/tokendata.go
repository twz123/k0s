// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"errors"
	"fmt"
	"os"

	"github.com/k0sproject/k0s/pkg/token"
)

// EnvVarToken is the environment variable name for the join token
const EnvVarToken = "K0S_TOKEN"

// CheckSingleTokenSource verifies that at most one token source is provided.
// Returns an error if multiple sources are specified.
func CheckSingleTokenSource(tokenArg, tokenFile string) error {
	tokenSources := 0
	if tokenArg != "" {
		tokenSources++
	}
	if tokenFile != "" {
		tokenSources++
	}
	if os.Getenv(EnvVarToken) != "" {
		tokenSources++
	}

	if tokenSources > 1 {
		return fmt.Errorf("you can only pass one token source: either as a CLI argument, via '--token-file [path]', or via the %s environment variable", EnvVarToken)
	}

	return nil
}

// GetTokenData resolves the join token from multiple possible sources:
// CLI argument, token file, or K0S_TOKEN environment variable.
// Returns the zero token if no token source is available.
func GetTokenData(tokenArg, tokenFile string) (token.JoinToken, error) {
	tokenEnvValue := os.Getenv(EnvVarToken)

	if tokenArg != "" {
		return decodeJoinToken(tokenArg)
	}

	if tokenEnvValue != "" {
		return decodeJoinToken(tokenEnvValue)
	}

	if tokenFile == "" {
		return token.JoinToken{}, nil
	}

	var problem string
	data, err := os.ReadFile(tokenFile)
	if errors.Is(err, os.ErrNotExist) {
		problem = "not found"
	} else if err != nil {
		return token.JoinToken{}, fmt.Errorf("failed to read token file: %w", err)
	} else if len(data) == 0 {
		problem = "is empty"
	}
	if problem != "" {
		return token.JoinToken{}, fmt.Errorf(`token file "%s" %s`+
			`: obtain a new token via "k0s token create ..." and store it in the file`+
			` or reinstall this node via "k0s install --force ..." or "k0sctl apply --force ..."`,
			tokenFile, problem)
	}
	return decodeJoinToken(string(data))
}

func decodeJoinToken(encoded string) (token.JoinToken, error) {
	joinToken, err := token.DecodeJoinToken(encoded)
	if err != nil {
		return token.JoinToken{}, fmt.Errorf("failed to decode join token: %w", err)
	}
	return joinToken, nil
}
