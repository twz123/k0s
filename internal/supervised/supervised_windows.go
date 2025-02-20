// Copyright 2025 k0s authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package supervised

import (
	"context"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows/svc"
)

func run(main MainFunc) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}

	if !isService {
		ctx, cancel := ShutdownContext(context.Background())
		defer cancel(nil)
		return main(ctx)
	}

	if err := runService(main); err != nil {
		// In case the service returns with an error,
		// log it, since stdout/stderr go into nirvana.
		logrus.WithError(err).Error("Terminating")
		return err
	}

	return nil
}
