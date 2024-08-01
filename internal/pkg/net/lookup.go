/*
Copyright 2024 k0s authors

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

package net

import (
	"context"
	"fmt"
	"net"
)

// Resolves the preferred loopback IP address for this machine.
//
// Performs a DNS lookup for "localhost" and returns the first address that is
// recognized as a loopback address. Returns an error if the DNS lookup fails
// (e.g. due to context cancellation) or the lookup didn't return a loopback
// address. In that case, a sane default loopback address is returned.
//
// Example usage:
//
//	ip, err := LookupLoopbackIP(ctx)
//	if err != nil {
//	    fmt.Fprintln(os.Stderr, "Failed to find loopback IP, falling back to", ip.String(), "-", err)
//	}
//	// ... use ip somehow
func LookupLoopbackIP(ctx context.Context) (net.IP, error) {
	localIPs, err := net.DefaultResolver.LookupIPAddr(ctx, "localhost")
	if err != nil {
		err = fmt.Errorf("failed to resolve localhost: %w", err)
	} else {
		for _, addr := range localIPs {
			if addr.IP.IsLoopback() {
				return addr.IP, nil
			}
		}

		err = fmt.Errorf("no loopback IPs found for localhost: %v", localIPs)
	}

	return net.IP{127, 0, 0, 1}, err
}
