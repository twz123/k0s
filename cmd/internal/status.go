// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"context"

	"github.com/k0sproject/k0s/pkg/component/status"

	"github.com/sirupsen/logrus"
)

// StartStatus starts serving the status socket and returns a function that
// stops it again.
func StartStatus(ctx context.Context, s *status.Status) (stop func(), _ error) {
	if err := s.Init(ctx); err != nil {
		return nil, err
	}
	if err := s.Start(ctx); err != nil {
		return nil, err
	}

	return func() {
		if err := s.Stop(); err != nil {
			logrus.WithError(err).Warn("Failed to stop status component")
		}
	}, nil
}
