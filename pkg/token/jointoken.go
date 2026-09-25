// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package token

import (
	"fmt"

	"github.com/k0sproject/k0s/internal/secret"
)

// A kubeconfig with bootstrap credentials for joining new nodes.
type JoinToken struct {
	kubeconfig secret.Value[JoinToken, []byte]
}

// NoStringError is the [secret.NoValueError] for the zero [JoinToken].
type NoJoinTokenError = secret.NoValueError[JoinToken]

// String implements [fmt.Stringer]. It never reveals the token.
func (t JoinToken) String() string { return t.kubeconfig.String() }

// Format implements [fmt.Formatter]. It never reveals the token.
func (t JoinToken) Format(f fmt.State, verb rune) { t.kubeconfig.Format(f, verb) }
