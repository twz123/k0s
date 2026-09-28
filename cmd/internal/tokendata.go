// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"errors"
	"fmt"
	"os"
)

// EnvVarToken is the environment variable name for the join token
const EnvVarToken = "K0S_TOKEN"

// GetJoinTokenSource picks the join token source from the CLI argument, the
// token file and the K0S_TOKEN environment variable. It returns an error if
// more than one source is given, nil if none is, and otherwise a function that
// loads the token data from the selected source.
func GetJoinTokenSource(tokenArg, tokenFile string) (func() (string, error), error) {
	envToken := os.Getenv(EnvVarToken)

	var sources uint
	for _, source := range []string{tokenArg, tokenFile, envToken} {
		if source != "" {
			sources++
		}
	}
	if sources > 1 {
		return nil, fmt.Errorf("you can only pass one token source: either as a CLI argument, via '--token-file [path]', or via the %s environment variable", EnvVarToken)
	}

	switch {
	case tokenArg != "":
		return func() (string, error) { return tokenArg, nil }, nil
	case envToken != "":
		return func() (string, error) { return envToken, nil }, nil
	case tokenFile != "":
		return func() (string, error) { return readTokenFile(tokenFile) }, nil
	default:
		return nil, nil
	}
}

func readTokenFile(tokenFile string) (string, error) {
	var problem string
	data, err := os.ReadFile(tokenFile)
	if errors.Is(err, os.ErrNotExist) {
		problem = "not found"
	} else if err != nil {
		return "", fmt.Errorf("failed to read token file: %w", err)
	} else if len(data) == 0 {
		problem = "is empty"
	}
	if problem != "" {
		return "", fmt.Errorf(`token file "%s" %s`+
			`: obtain a new token via "k0s token create ..." and store it in the file`+
			` or reinstall this node via "k0s install --force ..." or "k0sctl apply --force ..."`,
			tokenFile, problem)
	}
	return string(data), nil
}
