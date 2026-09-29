// SPDX-FileCopyrightText: 2022 k0s authors
// SPDX-License-Identifier: Apache-2.0

package status

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/prober"
	"github.com/k0sproject/k0s/pkg/config"
)

type K0sStatus struct {
	Version                     string
	Pid                         int
	PPid                        int
	Role                        string
	SysInit                     string
	StubFile                    string
	Output                      string
	Workloads                   bool
	SingleNode                  bool
	Args                        []string
	WorkerToAPIConnectionStatus ProbeStatus
	ClusterConfig               *v1beta1.ClusterConfig
	K0sVars                     *config.CfgVars
}
type ProbeStatus struct {
	Message string
	Success bool
}

// GetStatus returns the status of the k0s process using the status socket
func GetStatusInfo(socketPath string) (*K0sStatus, error) {
	status := &K0sStatus{}
	if err := doStatusHTTPRequest(socketPath, "status", status); err != nil {
		return nil, err
	}
	return status, nil
}

// GetStatusInfoWithoutProbe returns the status of the k0s process using the
// status socket, without probing the connection from the worker to the API
// server. The probe may block for a long time, e.g. while kubelet isn't yet
// bootstrapped, whereas this call returns as soon as the socket is served.
// The returned status leaves [K0sStatus.WorkerToAPIConnectionStatus] empty.
func GetStatusInfoWithoutProbe(socketPath string) (*K0sStatus, error) {
	status := &K0sStatus{}
	if err := doStatusHTTPRequest(socketPath, "status?probe=false", status); err != nil {
		return nil, err
	}
	return status, nil
}

// GetComponentStatus returns the per-component events and health-checks
func GetComponentStatus(socketPath string, maxCount int) (*prober.State, error) {
	status := &prober.State{}
	if err := doStatusHTTPRequest(socketPath,
		fmt.Sprintf("components?maxCount=%d", maxCount),
		status); err != nil {
		return nil, err
	}
	return status, nil
}

func doStatusHTTPRequest(socketPath string, path string, tgt any) error {
	httpc := &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialSocket(ctx, socketPath)
			},
		},
	}

	response, err := httpc.Get("http://localhost/" + path)
	if err != nil {
		return fmt.Errorf("status: can't get %q via %q: %w", path, socketPath, err)
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status: can't get %q via %q: status code %d", path, socketPath, response.StatusCode)
	}

	if err := json.NewDecoder(response.Body).Decode(tgt); err != nil {
		return fmt.Errorf("status: can't get %q via %q: can't decode JSON: %w", path, socketPath, err)
	}

	return nil
}
