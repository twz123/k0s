// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package secret_test

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/k0sproject/k0s/internal/secret"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A secret string, the way secret values are meant to be declared.
type token struct {
	secret.Value[token, string]
}

// A secret byte slice.
type blob struct {
	secret.Value[blob, []byte]
}

func TestValue(t *testing.T) {
	t.Run("reveals the value", func(t *testing.T) {
		tok := token{secret.Of[token]("hunter2")}
		revealed, err := tok.Reveal()
		require.NoError(t, err)
		assert.Equal(t, "hunter2", revealed)

		b := blob{secret.Of[blob]([]byte{1, 2, 3})}
		revealedBytes, err := b.Reveal()
		require.NoError(t, err)
		assert.Equal(t, []byte{1, 2, 3}, revealedBytes)
	})

	t.Run("holds no value when zero", func(t *testing.T) {
		revealed, err := token{}.Reveal()
		assert.Empty(t, revealed)
		var noValue secret.NoValueError[token]
		if assert.ErrorAs(t, err, &noValue) {
			assert.Equal(t, "no secret_test.token", noValue.Error())
		}
		assert.NotErrorAs(t, err, new(secret.NoValueError[blob]), "Error should be specific to the kind")
	})

	t.Run("is named after the embedding type", func(t *testing.T) {
		assert.Equal(t, "<secret_test.token>", token{}.String())
		assert.Equal(t, "<secret_test.blob>", blob{}.String())
	})

	t.Run("is redacted when formatted", func(t *testing.T) {
		value := []byte("hunter2")
		b := blob{secret.Of[blob](value)}

		// The ways in which the value could be spelled out.
		revealing := []string{string(value), fmt.Sprint(value), hex.EncodeToString(value)}

		for verb, expected := range map[string]string{
			"%s":  "<secret_test.blob>",
			"%v":  "<secret_test.blob>",
			"%+v": "<secret_test.blob>",
			"%#v": "<secret_test.blob>",
			"%q":  `"<secret_test.blob>"`,
			"%x":  "%!x(secret_test.blob)",
			"%d":  "%!d(secret_test.blob)",
			"%c":  "%!c(secret_test.blob)",
		} {
			formatted := fmt.Sprintf(verb, b)
			assert.Equal(t, expected, formatted, "Formatting via %s should not reveal the value", verb)
			formatted = fmt.Sprintf(verb, &b)
			assert.Equal(t, expected, formatted, "Formatting a pointer via %s should not reveal the value", verb)
			formatted = fmt.Sprintf(verb, b.Value)
			assert.Equal(t, expected, formatted, "Formatting the embedded value via %s should not reveal the value", verb)
		}

		// Values reached via unexported fields are printed via reflection,
		// which bypasses any methods. The best that can be done there is to
		// show an address.
		type holder struct{ b blob }
		for _, verb := range []string{"%v", "%+v", "%#v", "%d"} {
			formatted := fmt.Sprintf(verb, holder{b})
			for _, revealed := range revealing {
				assert.NotContains(t, formatted, revealed, "Formatting via %s should not reveal the value", verb)
			}
		}
	})
}
