/*
Copyright 2025 k0s authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package internal

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/k0sproject/k0s/pkg/join"
	"github.com/spf13/pflag"
)

type TokenFlags struct {
	tokenFile string
}

func (f *TokenFlags) AddToFlagSet(flags *pflag.FlagSet) {
	flags.StringVar(&f.tokenFile, "token-file", "", "Path to the file containing join-token.")
}

func (f *TokenFlags) GetSource(args []string) (join.TokenSource, error) {
	path := f.tokenFile

	if len(args) > 0 {
		if path != "" {
			return nil, errors.New("a join token can be either specified as an argument or via the --token-file flag, not both")
		}

		token, err := join.DecodeToken(args[0])
		if err != nil {
			return nil, fmt.Errorf("failed to decode join token: %w", err)
		}

		return (*staticToken)(token), nil
	}

	if path != "" {
		return tokenFile(path), nil
	}

	return nil, nil
}

type staticToken join.Token

func (t *staticToken) ReadToken(context.Context) (*join.Token, error) {
	var copy join.Token = *(*join.Token)(t)
	return &copy, nil
}

type tokenFile string

func (f tokenFile) ReadToken(context.Context) (*join.Token, error) {
	var problem string
	data, err := os.ReadFile(string(f))
	if errors.Is(err, os.ErrNotExist) {
		problem = "not found"
	} else if err != nil {
		return nil, fmt.Errorf("failed to read token file: %w", err)
	} else if len(data) == 0 {
		problem = "is empty"
	}

	if problem != "" {
		return nil, fmt.Errorf("token file %q %s"+
			`: obtain a new token via "k0s token create ..." and store it in the file`+
			` or reinstall this node via "k0s install --force ..." or "k0sctl apply --force ..."`,
			f, problem)
	}

	token, err := join.DecodeToken(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode join token stored in %s: %w", f, err)
	}

	return token, nil
}
