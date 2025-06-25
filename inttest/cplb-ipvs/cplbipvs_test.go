// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package keepalived

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/k0sproject/k0s/inttest/common"

	"github.com/stretchr/testify/suite"
)

type cplbIPVSSuite struct {
	common.BootlooseSuite
}

const haControllerConfig = `
spec:
  network:
    controlPlaneLoadBalancing:
      enabled: true
      type: Keepalived
      keepalived:
        vrrpInstances:
        - virtualIPs: ["%s/16"]
          authPass: "123456"
          unicastSourceIP: %s
          unicastPeers: [%s, %s]
        virtualServers:
        - ipAddress: %s
    nodeLocalLoadBalancing:
      enabled: true
      type: EnvoyProxy
`

// SetupTest prepares the controller and filesystem, getting it into a consistent
// state which we can run tests against.
func (s *cplbIPVSSuite) TestK0sGetsUp() {
	lb := s.getLBAddress()
	ctx := s.Context()
	var joinToken string

	for idx := range s.ControllerCount {
		s.Require().NoError(s.WaitForSSH(s.ControllerNode(idx), 2*time.Minute, 1*time.Second))
		addr := s.getUnicastAddresses(idx)
		s.PutFile(s.ControllerNode(idx), "/tmp/k0s.yaml",
			fmt.Sprintf(haControllerConfig, lb, addr[0], addr[1], addr[2], lb))

		// Note that the token is intentionally empty for the first controller
		s.Require().NoError(s.InitController(idx, "--config=/tmp/k0s.yaml", "--disable-components=metrics-server", joinToken))
		s.Require().NoError(s.WaitJoinAPI(s.ControllerNode(idx)))

		// With the primary controller running, create the join token for subsequent controllers.
		if idx == 0 {
			token, err := s.GetJoinToken("controller")
			s.Require().NoError(err)
			joinToken = token
		}
	}

	// Final sanity -- ensure all nodes see each other according to etcd
	for idx := range s.ControllerCount {
		s.Require().Len(s.GetMembers(idx), s.ControllerCount)
	}

	// Create a worker join token
	workerJoinToken, err := s.GetJoinToken("worker")
	s.Require().NoError(err)

	// Start the workers using the join token
	s.Require().NoError(s.RunWorkersWithToken(workerJoinToken))

	client, err := s.KubeClient(s.ControllerNode(0))
	s.Require().NoError(err)

	s.Require().NoError(s.WaitForNodeReady(s.WorkerNode(0), client))

	// Verify that all servers have the dummy interface
	for idx := range s.ControllerCount {
		s.checkDummy(ctx, s.ControllerNode(idx), lb)
	}

	// Verify that only one controller has the VIP in eth0
	activeNode := -1
	count := 0
	for idx := range s.ControllerCount {
		if s.hasVIP(ctx, s.ControllerNode(idx), lb) {
			activeNode = idx
			count++
		}
	}
	s.Require().Equal(1, count, "Expected exactly one controller to have the VIP")

	// Verify that the real servers are present in the ipvsadm output in the active node and missing in the others
	for idx := range s.ControllerCount {
		s.validateRealServers(ctx, s.ControllerNode(idx), lb, idx == activeNode)
	}
}

// getLBAddress returns the IP address of the controller 0 and it adds 100 to
// the last octet unless it's bigger or equal to 154, in which case it
// subtracts 100. Theoretically this could result in an invalid IP address.
func (s *cplbIPVSSuite) getLBAddress() string {
	ip := s.GetIPAddress(s.ControllerNode(0))
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		s.T().Fatalf("Invalid IP address: %q", ip)
	}
	lastOctet, err := strconv.Atoi(parts[3])
	s.Require().NoErrorf(err, "Failed to convert last octet %q to int", parts[3])
	if lastOctet >= 154 {
		lastOctet -= 100
	} else {
		lastOctet += 100
	}

	return fmt.Sprintf("%s.%d", strings.Join(parts[:3], "."), lastOctet)
}

// getUnicastAddresses returns the IP addresses of the controllers. The first IP
// is the address of the controller with the ID provided.
func (s *cplbIPVSSuite) getUnicastAddresses(i int) []string {
	return []string{
		s.GetIPAddress(s.ControllerNode(i % s.ControllerCount)),
		s.GetIPAddress(s.ControllerNode((i + 1) % s.ControllerCount)),
		s.GetIPAddress(s.ControllerNode((i + 2) % s.ControllerCount)),
	}
}

// validateRealServers checks that the real servers are present in the
// ipvsadm output.
func (s *cplbIPVSSuite) validateRealServers(ctx context.Context, node string, vip string, isActive bool) {
	ssh, err := s.SSH(ctx, node)
	s.Require().NoError(err)
	defer ssh.Disconnect()

	servers := []string{}
	for i := range s.ControllerCount {
		servers = append(servers, s.GetIPAddress(s.ControllerNode(i)))
	}

	output, err := ssh.ExecWithOutput(ctx, "ipvsadm --save -n")
	s.Require().NoError(err)

	if isActive {
		for _, server := range servers {
			s.Require().Containsf(output, fmt.Sprintf("-a -t %s:6443 -r %s", vip, server), "Controller %s is missing a server in ipvsadm", node)
		}
	} else {
		for _, server := range servers {
			s.Require().NotContainsf(output, fmt.Sprintf("-a -t %s:6443 -r %s", vip, server), "Controller %s has a server in ipvsadm", node)
		}
	}

}

// checkDummy checks that the dummy interface is present on the given node and
// that it has only the virtual IP address.
func (s *cplbIPVSSuite) checkDummy(ctx context.Context, node string, vip string) {
	ssh, err := s.SSH(ctx, node)
	s.Require().NoError(err)
	defer ssh.Disconnect()

	output, err := ssh.ExecWithOutput(ctx, "ip --oneline addr show dummyvip0")
	s.Require().NoError(err)

	s.Require().Equal(0, strings.Count(output, "\n"), "Expected only one line of output")

	expected := fmt.Sprintf("inet %s/32", vip)
	s.Require().Contains(output, expected)
}

// hasVIP checks that the dummy interface is present on the given node and
// that it has only the virtual IP address.
func (s *cplbIPVSSuite) hasVIP(ctx context.Context, node string, vip string) bool {
	ssh, err := s.SSH(ctx, node)
	s.Require().NoError(err)
	defer ssh.Disconnect()

	output, err := ssh.ExecWithOutput(ctx, "ip --oneline addr show eth0")
	s.Require().NoError(err)

	return strings.Contains(output, fmt.Sprintf("inet %s/16", vip))
}

// TestKeepAlivedSuite runs the keepalived test suite. It verifies that the
// virtual IP is working by joining a node to the cluster using the VIP.
func TestCPLBIPVSSuite(t *testing.T) {
	suite.Run(t, &cplbIPVSSuite{
		common.BootlooseSuite{
			ControllerCount: 3,
			WorkerCount:     1,
		},
	})
}
