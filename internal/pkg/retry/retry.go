/*
Copyright 2022 k0s authors

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

package retry

import (
	"context"
	"math"
	"time"

	"github.com/avast/retry-go"
)

// UntilDone retries the given fn indefinitely until it returns nil or the given
// context is done.
func UntilDone(ctx context.Context, fn func(context.Context) error, opts ...retry.Option) error {

	// Options that can be overridden by callers.
	defaultOpts := []retry.Option{
		// Since the retries will be potentially indefinite, it does make sense
		// to define a max delay. The default delay is 100 milliseconds and the
		// default delay type is a simple exponential backoff, so that the one
		// minute cap will be reached after the tenth retry. Callers are free to
		// chose another MaxDelay, though.
		retry.MaxDelay(1 * time.Minute),
	}

	// Options that will override the caller options.
	overrideOpts := []retry.Option{
		// Let the retries stop when ctx is done.
		retry.Context(ctx),

		// This should make the retries indefinite in practice. There's
		// still the theoretical possibility that the retry loop ends
		// after MaxUint attempts. Assumed that a single retry takes one
		// millisecond, the retries will end after 49 days on 32 bit
		// systems. Hence the for loop later on...
		retry.Attempts(math.MaxUint),

		// Don't collect all the errors, just the last error is the
		// interesting one.
		retry.LastErrorOnly(true),
	}

	opts = append(append(defaultOpts, opts...), overrideOpts...)

	for {
		var lastErr error
		retryErr := retry.Do(func() error { lastErr := fn(ctx); return lastErr }, opts...)
		if retryErr == nil {
			return nil
		}

		if ctx.Err() != nil {
			// Prefer lastErr over retryErr: In case the context was cancelled
			// or timed out, retryErr will just be the err of the context, but
			// not the last encountered err of the retried function.
			if lastErr != nil {
				return lastErr
			}
			return retryErr
		}
	}
}
