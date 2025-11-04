// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package hacontrolplane

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/k0sproject/k0s/inttest/common"
	aptest "github.com/k0sproject/k0s/inttest/common/autopilot"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	etcdv1beta1 "github.com/k0sproject/k0s/pkg/apis/etcd/v1beta1"
	clientetcdv1beta1 "github.com/k0sproject/k0s/pkg/client/clientset/typed/etcd/v1beta1"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"
)

const basePath = "apis/etcd.k0sproject.io/v1beta1/etcdmembers/%s"

type EtcdMemberSuite struct {
	common.BootlooseSuite
}

func (s *EtcdMemberSuite) getMembers(ctx context.Context, fromControllerIdx int) map[string]string {
	// our etcd instances doesn't listen on public IP, so test is performed by calling CLI tools over ssh
	// which in general even makes sense, we can test tooling as well
	sshCon, err := s.SSH(ctx, s.ControllerNode(fromControllerIdx))
	s.Require().NoError(err)
	defer sshCon.Disconnect()
	output, err := sshCon.ExecWithOutput(ctx, "/usr/local/bin/k0s etcd member-list 2>/dev/null")
	s.T().Logf("k0s etcd member-list output: %s", output)
	s.Require().NoError(err)

	members := struct {
		Members map[string]string `json:"members"`
	}{}

	s.NoError(json.Unmarshal([]byte(output), &members))
	return members.Members
}

func (s *EtcdMemberSuite) TestDeregistration() {
	ctx := s.Context()
	var joinToken string
	for idx := range s.ControllerCount {
		nodeName := s.ControllerNode(idx)
		// s.Require().NoError(s.WaitForSSH(nodeName, 2*time.Minute, 1*time.Second))

		// Note that the token is intentionally empty for the first controller
		s.Require().NoError(s.InitController(idx, joinToken))
		if joinToken == "" {
			// With the primary controller running, create the join token for subsequent controllers.
			s.Require().NoError(s.WaitJoinAPI(nodeName))
			var err error
			joinToken, err = s.GetJoinToken("controller")
			s.Require().NoError(err)
		}
	}

	s.T().Log("Waiting for EtcdMembers CRD")
	extClient, err := s.ExtensionsClient(s.ControllerNode(0))
	s.Require().NoError(err)
	s.Require().NoError(aptest.WaitForCRDByGroupName(ctx, extClient, "etcdmembers."+etcdv1beta1.GroupName))

	// Final sanity -- ensure all nodes see each other according to etcd
	for idx := range s.ControllerCount {
		s.Require().Len(s.GetMembers(idx), s.ControllerCount)
	}
	kc, err := s.KubeClient(s.ControllerNode(0))
	s.Require().NoError(err)

	memberClient, err := s.EtcdMemberClient(s.ControllerNode(0))

	// Check each node is present in the etcd cluster and reports joined state
	// Use errorgroup to wait for all the statuses to be updated
	eg, ectx := errgroup.WithContext(ctx)

	for i := range s.ControllerCount {
		obj := s.ControllerNode(i)
		ctx := ectx
		eg.Go(func() error {
			s.T().Logf("verifying initial status of %s", obj)

			var em *etcdv1beta1.EtcdMember
			if err := s.waitForMember(ctx, memberClient.EtcdMembers(), obj, func(item *etcdv1beta1.EtcdMember) (bool, error) {
				for _, cond := range item.Status.Conditions {
					if cond.Type == etcdv1beta1.ConditionTypeJoined {
						em = item
						return true, nil
					}
				}
				return false, nil
			}); err != nil {
				return err
			}
			if em.Status.PeerAddress != s.GetControllerIPAddress(i) {
				return fmt.Errorf("expected PeerAddress %s, got %s", s.GetControllerIPAddress(i), em.Status.PeerAddress)
			}

			c := em.Status.GetCondition(etcdv1beta1.ConditionTypeJoined)
			if c == nil {
				return fmt.Errorf("expected condition %s, got nil", etcdv1beta1.ConditionTypeJoined)
			}
			if c.Status != etcdv1beta1.ConditionTrue {
				return fmt.Errorf("expected condition %s to be %s, got %s", etcdv1beta1.ConditionTypeJoined, etcdv1beta1.ConditionTrue, c.Status)
			}
			return nil
		})

	}

	s.T().Log("waiting to see correct statuses on EtcdMembers")
	s.NoError(eg.Wait())
	s.T().Log("All statuses found")
	// Make one of the nodes leave
	s.leaveNode(ctx, "controller2")

	// Check that the node is gone from the etcd cluster according to etcd itself
	members := s.getMembers(ctx, 0)
	s.Require().Len(members, s.ControllerCount-1)
	s.Require().NotContains(members, "controller2")

	// Make sure the EtcdMember CR status is successfully updated
	em := s.getMember(ctx, "controller2")
	s.Require().Equal(etcdv1beta1.ReconcileStatusSuccess, em.Status.ReconcileStatus)
	s.Require().Equal(etcdv1beta1.ConditionFalse, em.Status.GetCondition(etcdv1beta1.ConditionTypeJoined).Status)

	// Stop k0s and reset the node
	s.Require().NoError(s.StopController(s.ControllerNode(2)))
	s.Require().NoError(common.ResetNode(s.ControllerNode(2), &s.BootlooseSuite))

	// Make the node rejoin
	s.Require().NoError(s.InitController(2, joinToken))
	s.Require().NoError(s.WaitForKubeAPI(s.ControllerNode(2)))

	s.T().Log("Waiting for ", s.ControllerNode(2), " to re-join as an etcd member")
	s.waitForMember(ctx, memberClient.EtcdMembers(), s.ControllerNode(2), func(em *etcdv1beta1.EtcdMember) (bool, error) {
		if !em.Spec.Leave {
			for _, cond := range em.Status.Conditions {
				if cond.Type == etcdv1beta1.ConditionTypeJoined {
					return cond.Status == etcdv1beta1.ConditionTrue, nil
				}
			}
		}

		return false, nil
	})

	// Check that after restarting the controller, the member is still present
	s.Require().NoError(s.RestartController(s.ControllerNode(2)))
	em = &etcdv1beta1.EtcdMember{}
	err = kc.RESTClient().Get().AbsPath(fmt.Sprintf(basePath, "controller2")).Do(ctx).Into(em)
	s.Require().NoError(err)
	s.Require().Equal(em.Status.PeerAddress, s.GetControllerIPAddress(2))

	// Figure out what node is the leader and mark it as leaving
	leader := s.getLeader(ctx)
	s.leaveNode(ctx, leader)

}

// getLeader returns the name of the current k0s leader node by comparing
// the holder identity of the "k0s-endpoint-reconciler" lease to the per node leases
func (s *EtcdMemberSuite) getLeader(ctx context.Context) string {
	// First we need to get all leases in "kube-node-lease" NS
	kc, err := s.KubeClient(s.ControllerNode(0))
	s.Require().NoError(err)
	leases, err := kc.CoordinationV1().Leases("kube-node-lease").List(ctx, metav1.ListOptions{})
	s.Require().NoError(err)
	leaseIDs := make(map[string]string)
	for _, l := range leases.Items {
		if strings.Contains(l.Name, "k0s-ctrl") {
			node := strings.ReplaceAll(l.Name, "k0s-ctrl-", "")
			leaseID := l.Spec.HolderIdentity
			leaseIDs[*leaseID] = node
		}
	}
	// Next we need to match the "k0s-endpoint-reconciler" lease holder identity to a node name
	leaderLease, err := kc.CoordinationV1().Leases("kube-node-lease").Get(ctx, "k0s-endpoint-reconciler", metav1.GetOptions{})
	s.Require().NoError(err)
	return leaseIDs[*leaderLease.Spec.HolderIdentity]

}

func (s *EtcdMemberSuite) leaveNode(ctx context.Context, name string) {
	// Get kube client to some other node that we're marking to leave
	node := name
	for i := range s.ControllerCount {
		if candidate := s.ControllerNode(i); candidate != node {
			node = candidate
			break
		}
	}

	s.T().Logf("using %s as API server to mark %s for leaving", node, name)

	cfg, err := s.GetKubeConfig(node)
	s.Require().NoError(err)
	kc, err := kubernetes.NewForConfig(cfg)
	s.Require().NoError(err)
	ec, err := clientetcdv1beta1.NewForConfig(cfg)
	s.Require().NoError(err)

	// Patch the EtcdMember CR to set the Leave flag
	path := fmt.Sprintf(basePath, name)
	patch := []byte(`{"spec":{"leave":true}}`)
	result := kc.RESTClient().Patch("application/merge-patch+json").AbsPath(path).Body(patch).Do(ctx)

	s.Require().NoError(result.Error())
	s.T().Logf("marked %s for leaving, waiting to see the state updated", name)
	s.waitForMember(ctx, ec.EtcdMembers(), name, func(member *etcdv1beta1.EtcdMember) (bool, error) {
		for _, condition := range member.Status.Conditions {
			if condition.Type == etcdv1beta1.ConditionTypeJoined {
				s.T().Log(name, " ", etcdv1beta1.ConditionTypeJoined, ": ", condition.Status)
				return condition.Status == etcdv1beta1.ConditionFalse, nil
			}
		}

		s.T().Log(name, " has no ", etcdv1beta1.ConditionTypeJoined, " condition")
		return false, nil
	})
}

// getMember returns the etcd member CR for the given name
func (s *EtcdMemberSuite) getMember(ctx context.Context, name string) *etcdv1beta1.EtcdMember {
	kc, err := s.KubeClient(s.ControllerNode(0))
	s.Require().NoError(err)

	em := &etcdv1beta1.EtcdMember{}
	err = kc.RESTClient().Get().AbsPath(fmt.Sprintf(basePath, name)).Do(ctx).Into(em)
	s.Require().NoError(err)
	return em
}

func (s *EtcdMemberSuite) waitForMember(ctx context.Context, members clientetcdv1beta1.EtcdMemberInterface, name string, condition func(*etcdv1beta1.EtcdMember) (bool, error)) error {
	return watch.EtcdMembers(members).
		WithObjectName(name).
		WithErrorCallback(func(err error) (time.Duration, error) {
			if timeout, err := common.RetryWatchErrors(s.T().Logf)(err); err == nil {
				return timeout, nil
			}
			s.T().Logf("Encountered unknown error while watching, retrying in a sec: %v", err)
			return time.Second, nil
		}).
		Until(ctx, condition)
}

func TestEtcdMemberSuite(t *testing.T) {
	s := EtcdMemberSuite{
		common.BootlooseSuite{
			ControllerCount: 3,
			LaunchMode:      common.LaunchModeOpenRC,
			// WithLB: true,
		},
	}

	suite.Run(t, &s)

}
