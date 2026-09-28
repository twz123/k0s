// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package etcdlearner

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/k0sproject/k0s/inttest/common"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/token"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/stretchr/testify/suite"
)

// EtcdLearnerSuite covers the join-as-learner safety property: a peer
// added through the k0s join API with an unreachable peer URL must not
// break quorum on the existing cluster, and the reconciler must not
// promote it (since it can never catch up). This stands in for the
// wrong-interface scenario the learner-join change exists to fix.
type EtcdLearnerSuite struct {
	common.BootlooseSuite
}

const phantomPeerURL = "https://192.0.2.42:2380" // RFC 5737 TEST-NET-1, unrouteable

func (s *EtcdLearnerSuite) TestUnreachableLearnerPreservesQuorum() {
	ctx := s.Context()

	// Boot controller0 as a single-node cluster.
	s.Require().NoError(s.InitController(0, ""))
	s.Require().NoError(s.WaitJoinAPI(s.ControllerNode(0)))
	s.Require().NoError(s.WaitForKubeAPI(s.ControllerNode(0)))

	kc, err := s.KubeClient(s.ControllerNode(0))
	s.Require().NoError(err)

	// Baseline write on the single-node cluster.
	_, err = kc.CoreV1().ConfigMaps("default").Create(ctx,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "pre-phantom"}},
		metav1.CreateOptions{})
	s.Require().NoError(err, "single-node cluster must accept writes before any peer is added")

	// Register a phantom peer through the k0s join API. The phantom's
	// advertised peer URL is TEST-NET-1 (RFC 5737), guaranteed
	// unrouteable. With the join-as-learner change, the existing cluster
	// adds it as a non-voting learner; quorum stays at 1.
	s.callJoinEtcd(ctx, s.ControllerNode(0), v1beta1.EtcdRequest{
		Node:        "phantom",
		PeerAddress: phantomPeerURL,
	})

	ssh, err := s.SSH(ctx, s.ControllerNode(0))
	s.Require().NoError(err)
	defer ssh.Disconnect()

	// Wait until the phantom shows up in etcd's member list as a learner.
	// Using etcdctl rather than `k0s etcd member-list`: the phantom has
	// not (and cannot) self-report its name, and `k0s etcd member-list`
	// surfaces only the name→peerURL map, which drops unnamed members.
	s.Require().NoError(common.Poll(ctx, func(ctx context.Context) (bool, error) {
		for _, m := range s.listMembers(ctx, ssh) {
			if slices.Contains(m.PeerURLs, phantomPeerURL) {
				return m.IsLearner, nil
			}
		}
		return false, nil
	}), "phantom never appeared in etcd member list as learner")

	// The critical assertion: quorum stays at 1 and writes succeed. If
	// the join API had added the new member as a voter, the cluster would
	// have lost quorum due to that unreachable voter.
	_, err = kc.CoreV1().ConfigMaps("default").Create(ctx,
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "post-phantom"}},
		metav1.CreateOptions{})
	s.Require().NoError(err,
		"etcd quorum was lost after adding an unreachable peer; "+
			"the new member must enter as a non-voting learner")

	// Confirm that the leader's reconciler does not incorrectly promote a peer
	// that hasn't caught up yet. Check the etcd member list three times with a
	// ten-second delay between each check to ensure that the phantom remains a
	// learner for at least thirty seconds.
	s.T().Log("Verifying phantom stays a learner for at least 30 seconds")
	for range 3 {
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			s.FailNow("Interrupted while observing etcd members")
		}

		members := s.listMembers(ctx, ssh)
		phantomIndex := slices.IndexFunc(members, func(m etcdMember) bool {
			return slices.Contains(m.PeerURLs, phantomPeerURL)
		})
		s.Require().GreaterOrEqual(phantomIndex, 0, "Phantom disappeared from etcd member list mid-test")
		s.Require().True(members[phantomIndex].IsLearner, "Phantom was promoted to voter despite being unreachable")
	}
}

// etcdMember is a minimal subset of etcdctl's member-list -w json output.
type etcdMember struct {
	ID        uint64   `json:"ID"`
	Name      string   `json:"name"`
	PeerURLs  []string `json:"peerURLs"`
	IsLearner bool     `json:"isLearner"`
}

// listMembers returns the raw etcd member list via etcdctl. We can't use
// `k0s etcd member-list` here because it drops unnamed members (the
// phantom learner has no name).
func (s *EtcdLearnerSuite) listMembers(ctx context.Context, ssh *common.SSHConnection) []etcdMember {
	out, err := ssh.ExecWithOutput(ctx,
		"/opt/etcdctl member list -w json "+
			"--endpoints=https://127.0.0.1:2379 "+
			"--cacert=/var/lib/k0s/pki/etcd/ca.crt "+
			"--cert=/var/lib/k0s/pki/apiserver-etcd-client.crt "+
			"--key=/var/lib/k0s/pki/apiserver-etcd-client.key")
	s.Require().NoErrorf(err, "etcdctl member list failed: %s", out)

	var resp struct {
		Members []etcdMember `json:"members"`
	}
	s.Require().NoError(json.Unmarshal([]byte(out), &resp), "etcdctl member list JSON: %s", out)
	return resp.Members
}

// callJoinEtcd uses the k0s JoinClient to POST an EtcdRequest to the join
// API on the given node. A join token created on the node would point to the
// controller's container-internal IP, which is not reachable from the test
// host. So the test mints its own controller token, the way `k0s token
// pre-shared` does: it stores a bootstrap secret in the cluster and puts the
// host-mapped join URL into the kubeconfig (localhost is in the k0s-api cert's
// SAN list, so TLS still validates). The rest of the code path runs exactly as
// it does in production.
func (s *EtcdLearnerSuite) callJoinEtcd(ctx context.Context, node string, req v1beta1.EtcdRequest) {
	kubeConfig, err := s.GetKubeConfig(node)
	s.Require().NoError(err)
	s.Require().NotEmpty(kubeConfig.TLSClientConfig.CAData, "admin kubeconfig has no CA data to put into the join token")
	kc, err := kubernetes.NewForConfig(kubeConfig)
	s.Require().NoError(err)

	bootstrapSecret, bootstrapToken, err := token.RandomBootstrapSecret(token.RoleController, 10*time.Minute)
	s.Require().NoError(err)
	_, err = kc.CoreV1().Secrets(metav1.NamespaceSystem).Create(ctx, bootstrapSecret, metav1.CreateOptions{})
	s.Require().NoError(err)

	m, err := s.MachineForName(node)
	s.Require().NoError(err)
	hostPort, err := m.HostPort(s.K0sAPIExternalPort)
	s.Require().NoError(err)
	joinURL := fmt.Sprintf("https://localhost:%d", hostPort)

	joinToken, err := token.GenerateJoinToken(joinURL, kubeConfig.TLSClientConfig.CAData, token.ControllerTokenAuthName, bootstrapToken)
	s.Require().NoError(err)
	client, err := joinToken.NewJoinClient()
	s.Require().NoError(err)

	resp, err := client.JoinEtcd(ctx, req)
	s.Require().NoError(err)
	s.Require().NotEmpty(resp.InitialCluster, "join API returned no initial-cluster")
}

func TestEtcdLearnerSuite(t *testing.T) {
	s := EtcdLearnerSuite{
		common.BootlooseSuite{
			ControllerCount: 1,
			LaunchMode:      common.LaunchModeOpenRC,
		},
	}
	suite.Run(t, &s)
}
