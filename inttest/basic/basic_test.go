// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package basic

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/k0sproject/k0s/inttest/common"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	certutil "k8s.io/client-go/util/cert"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/suite"
)

type BasicSuite struct {
	common.BootlooseSuite
}

type (
	CSR     = certificatesv1.CertificateSigningRequest
	CSRList = certificatesv1.CertificateSigningRequestList
)

func (s *BasicSuite) TestK0sGetsUp() {
	ctx := s.Context()
	customDataDir := "/var/lib/k0s/custom-data-dir"

	// Create an empty file to prove that k0s manage to rewrite a partially written file
	ssh, err := s.SSH(ctx, s.ControllerNode(0))
	s.Require().NoError(err)
	defer ssh.Disconnect()
	_, err = ssh.ExecWithOutput(ctx, fmt.Sprintf("mkdir -p %s/bin && touch -t 202201010000 %s/bin/kube-apiserver", customDataDir, customDataDir))
	s.Require().NoError(err)
	_, err = ssh.ExecWithOutput(ctx, "touch -t 202201010000 "+s.K0sFullPath)
	s.Require().NoError(err)
	_, err = ssh.ExecWithOutput(ctx, "mkdir -p /run/k0s/konnectivity-server/ && touch -t 202201010000 /run/k0s/konnectivity-server/konnectivity-server.sock")
	s.Require().NoError(err)

	dataDirOpt := "--data-dir=" + customDataDir
	s.Require().NoError(s.InitController(0, dataDirOpt))

	token, err := s.GetJoinToken("worker", dataDirOpt)
	s.Require().NoError(err)
	s.NoError(s.RunWorkersWithToken(token, `--labels="k0sproject.io/foo=bar"`, `--kubelet-extra-args=" --address=0.0.0.0  --event-burst=10"`))

	kc, err := s.KubeClient(s.ControllerNode(0), dataDirOpt)
	if err != nil {
		s.FailNow("failed to obtain Kubernetes client", err)
	}
	restConfig, err := s.GetKubeConfig(s.ControllerNode(0), "")
	s.Require().NoError(err)

	err = s.WaitForNodeReady(s.WorkerNode(0), kc)
	s.NoError(err)

	if labels, err := s.GetNodeLabels(s.WorkerNode(0), kc); s.NoError(err) {
		s.Equal("bar", labels["k0sproject.io/foo"])
	}

	err = s.WaitForNodeReady(s.WorkerNode(1), kc)
	s.NoError(err)

	s.AssertSomeKubeSystemPods(kc)

	s.T().Log("waiting to see kube-router pods ready")
	s.NoError(common.WaitForKubeRouterReady(ctx, kc), "kube-router did not start")

	s.Require().NoError(s.checkCertPerms(ctx, s.ControllerNode(0)))

	s.T().Log("Waiting for all worker CSRs to be approved")
	s.Require().NoError(s.checkCSRs(ctx, kc))
	s.verifyKubeletServingCerts(ctx, kc, restConfig)

	s.Require().NoError(s.verifyKubeletAddressFlag(ctx, s.WorkerNode(0)))
	s.Require().NoError(s.verifyKubeletAddressFlag(ctx, s.WorkerNode(1)))
	for _, lease := range []string{"kube-scheduler", "kube-controller-manager"} {
		s.T().Logf("Waiting for %s lease", lease)
		_, err := common.WaitForLease(ctx, kc, lease, metav1.NamespaceSystem)
		s.Require().NoError(err, lease)
	}

	s.Require().NoError(common.VerifyKonnectivityMesh(ctx, restConfig, kc, s.T(), uint(s.ControllerCount), uint(s.WorkerCount)), "While verifying konnectivity mesh")
	var wg sync.WaitGroup
	for i := range s.WorkerCount {
		t, node := s.T(), s.WorkerNode(i)
		wg.Go(func() {
			if verifyCAdvisorMetrics(ctx, t, kc, node) {
				t.Log("Verified cAdvisor metrics on", node)
			}
		})
	}
	defer wg.Wait()

	s.T().Log("checking kube-router gobgp functionality")
	kubeRouterPods, err := kc.CoreV1().Pods(metav1.NamespaceSystem).List(ctx, metav1.ListOptions{LabelSelector: "k8s-app=kube-router"})
	s.Require().NoError(err)
	// Just take the first running pod for execing the gobgp command
	for _, pod := range kubeRouterPods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			out, err := common.PodExecCmdOutput(kc, restConfig, pod.Name, metav1.NamespaceSystem, "gobgp global")
			s.Require().NoError(err)
			// Check that the output contains the default AS number, that's a sign that gobgp is working
			s.Regexp(`AS:\s+64512`, out)
			break
		}
	}

	s.verifyContainerdDefaultConfig(ctx)

	s.Require().NoError(s.probeCoreDNSAntiAffinity(ctx, kc))
}

func (s *BasicSuite) checkCertPerms(ctx context.Context, node string) error {
	ssh, err := s.SSH(ctx, node)
	if err != nil {
		return err
	}
	defer ssh.Disconnect()

	// Check that all .key files have 640 permissions
	keyOutput, err := ssh.ExecWithOutput(ctx, `find /var/lib/k0s/custom-data-dir/pki/ -name '*.key' -a \! -perm 0640`)
	if err != nil {
		return err
	}

	if keyOutput != "" {
		return fmt.Errorf("some private key files having non 640 permissions: %s", keyOutput)
	}

	// Check that .conf files have either 640 or 600 permissions (admin.conf, scheduler.conf, and ccm.conf use 600, others use 640)
	confOutput, err := ssh.ExecWithOutput(ctx, `find /var/lib/k0s/custom-data-dir/pki/ -name '*.conf' -a \! -perm 0640 -a \! -perm 0600`)
	if err != nil {
		return err
	}

	if confOutput != "" {
		return fmt.Errorf("some private conf files having non 640/600 permissions: %s", confOutput)
	}

	return nil
}

// Verifies that kubelet process has the address flag set
func (s *BasicSuite) verifyKubeletAddressFlag(ctx context.Context, node string) error {
	ssh, err := s.SSH(ctx, node)
	if err != nil {
		return err
	}
	defer ssh.Disconnect()

	output, err := ssh.ExecWithOutput(ctx, `grep -e '--address=0.0.0.0' /proc/$(pidof kubelet)/cmdline`)
	if err != nil {
		return err
	}
	if output != "--address=0.0.0.0" {
		return errors.New("kubelet does not have the address flag set")
	}

	return nil
}

func (s *BasicSuite) checkCSRs(ctx context.Context, kc *kubernetes.Clientset) error {
	// Wait until CSRs for all worker nodes got signed
	approvedNodes := map[string]struct{}{}

	return watch.FromClient[*CSRList, CSR](kc.CertificatesV1().CertificateSigningRequests()).
		WithFieldSelector(fields.OneTermEqualSelector("spec.signerName", "kubernetes.io/kubelet-serving")).
		WithErrorCallback(common.RetryWatchErrors(s.T().Logf)).
		Until(ctx, func(csr *CSR) (bool, error) {
			if !strings.HasPrefix(csr.Spec.Username, "system:node:worker") {
				return false, nil
			}
			if _, alreadyApproved := approvedNodes[csr.Spec.Username]; alreadyApproved {
				return false, nil
			}

			if reason, ok := getCSRApprovalReason(csr); !ok {
				s.T().Logf("CSR for %s is not yet approved", csr.Spec.Username)
				return false, nil
			} else if reason != "Autoapproved by K0s CSRApprover" {
				return false, fmt.Errorf("CSR for %s has an unexpected approval reason: %q", csr.Spec.Username, reason)
			}

			s.T().Logf("CSR for %s is approved", csr.Spec.Username)

			approvedNodes[csr.Spec.Username] = struct{}{}
			if len(approvedNodes) == s.WorkerCount {
				return true, nil
			}

			return false, nil
		})
}

// The subject common name of the kubelet-serving CA. This is wire format, so
// it's spelled out here rather than taken from the code under test.
const kubeletServingCACommonName = "kubernetes-kubelet-serving-ca"

// Verifies that the kubelets' serving certificates are issued by the
// kubelet-serving CA, and that the trust bundle for them is published.
func (s *BasicSuite) verifyKubeletServingCerts(ctx context.Context, kc *kubernetes.Clientset, restConfig *rest.Config) {
	s.T().Log("Waiting for the kubelet-serving CA trust bundle to be published")
	var trustBundle []byte
	s.Require().NoError(common.Poll(ctx, func(ctx context.Context) (bool, error) {
		configMap, err := kc.CoreV1().ConfigMaps(metav1.NamespaceSystem).Get(ctx, "kubelet-serving-ca.crt", metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		trustBundle = []byte(configMap.Data["ca.crt"])
		return true, nil
	}))

	trustBundleCerts, err := certutil.ParseCertsPEM(trustBundle)
	s.Require().NoError(err)
	s.Require().Len(trustBundleCerts, 2, "Trust bundle should hold the kubelet-serving CA and the cluster CA")
	s.Equal(kubeletServingCACommonName, trustBundleCerts[0].Subject.CommonName, "Trust bundle should start with the kubelet-serving CA")
	clusterCACerts, err := certutil.ParseCertsPEM(restConfig.CAData)
	s.Require().NoError(err)
	s.Require().Len(clusterCACerts, 1)
	s.Equal(clusterCACerts[0].Raw, trustBundleCerts[1].Raw, "Trust bundle should end with the cluster CA")

	trusted, clusterCA := x509.NewCertPool(), x509.NewCertPool()
	for _, cert := range trustBundleCerts {
		trusted.AddCert(cert)
	}
	clusterCA.AddCert(clusterCACerts[0])

	for i := range s.WorkerCount {
		node := s.WorkerNode(i)
		s.T().Logf("Waiting for the serving certificate of %s to be issued", node)
		var servingCert *x509.Certificate
		s.Require().NoError(common.Poll(ctx, func(ctx context.Context) (bool, error) {
			csrs, err := kc.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{
				FieldSelector: fields.OneTermEqualSelector("spec.signerName", "kubernetes.io/kubelet-serving").String(),
			})
			if err != nil {
				return false, err
			}
			for _, csr := range csrs.Items {
				if csr.Spec.Username != "system:node:"+node || len(csr.Status.Certificate) == 0 {
					continue
				}
				certs, err := certutil.ParseCertsPEM(csr.Status.Certificate)
				if err != nil {
					return false, err
				}
				servingCert = certs[0]
				return true, nil
			}
			return false, nil
		}))

		s.Equal(kubeletServingCACommonName, servingCert.Issuer.CommonName, "Serving certificate of %s should be issued by the kubelet-serving CA", node)
		_, err = servingCert.Verify(x509.VerifyOptions{Roots: trusted, DNSName: node})
		s.NoError(err, "Serving certificate of %s should verify against the trust bundle", node)
		_, err = servingCert.Verify(x509.VerifyOptions{Roots: clusterCA, DNSName: node})
		s.Error(err, "Serving certificate of %s should not verify against the cluster CA", node)
	}

	s.T().Log("Waiting for the kubelet-serving CA ClusterTrustBundle to be published")
	s.Require().NoError(common.Poll(ctx, func(ctx context.Context) (bool, error) {
		ctb, err := kc.CertificatesV1().ClusterTrustBundles().Get(ctx, "kubernetes.io:kubelet-serving:k0s", metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		s.Equal(string(trustBundle), ctb.Spec.TrustBundle, "ClusterTrustBundle should hold the same bundle as the ConfigMap")
		return true, nil
	}))
}

func getCSRApprovalReason(csr *CSR) (string, bool) {
	for _, condition := range csr.Status.Conditions {
		if condition.Type != certificatesv1.CertificateApproved {
			continue
		}
		return condition.Reason, true
	}

	return "", false
}

func (s *BasicSuite) verifyContainerdDefaultConfig(ctx context.Context) {
	var defaultConfig bytes.Buffer
	ssh, err := s.SSH(ctx, s.WorkerNode(0))
	if !s.NoError(err) {
		return
	}
	defer ssh.Disconnect()

	if !s.NoError(ssh.Exec(ctx, "/var/lib/k0s/bin/containerd --config=/etc/k0s/containerd.toml config dump", common.SSHStreams{Out: &defaultConfig})) {
		return
	}

	var parsedConfig struct {
		Plugins struct {
			CRIIMages struct {
				PinnedImages struct {
					Sandbox string `toml:"sandbox"`
				} `toml:"pinned_images"`
			} `toml:"io.containerd.cri.v1.images"`
		} `toml:"plugins"`
	}

	_, err = toml.Decode(defaultConfig.String(), &parsedConfig)
	if !s.NoError(err) {
		return
	}

	s.Equal((&v1beta1.ImageSpec{
		Image:   constant.KubePauseContainerImage,
		Version: constant.KubePauseContainerImageVersion,
	}).URI(), parsedConfig.Plugins.CRIIMages.PinnedImages.Sandbox)
}

func (s *BasicSuite) probeCoreDNSAntiAffinity(ctx context.Context, kc *kubernetes.Clientset) error {
	// Wait until both CoreDNS Pods got assigned to a node
	pods := map[string]string{}

	return watch.Pods(kc.CoreV1().Pods(metav1.NamespaceSystem)).
		WithLabels(labels.Set{"k8s-app": "kube-dns"}).
		WithErrorCallback(common.RetryWatchErrors(s.T().Logf)).
		Until(ctx, func(pod *corev1.Pod) (bool, error) {
			// Keep waiting until there's anti-affinity and node assignment
			if a := pod.Spec.Affinity; a == nil || a.PodAntiAffinity == nil {
				s.T().Logf("Pod %s doesn't have any pod anti-affinity", pod.Name)
				return false, nil
			}
			nodeName := pod.Spec.NodeName
			if nodeName == "" {
				s.T().Logf("Pod %s not scheduled yet: %+v", pod.Name, pod.Status)
				return false, nil
			}

			if prevName, ok := pods[nodeName]; ok && pod.Name != prevName {
				return false, fmt.Errorf("multiple CoreDNS pods scheduled on node %s: %s and %s", nodeName, prevName, pod.Name)
			}

			s.T().Logf("Pod %s scheduled on %s", pod.Name, pod.Spec.NodeName)

			pods[nodeName] = pod.Name
			return len(pods) > 1, nil
		})
}

func TestBasicSuite(t *testing.T) {
	s := BasicSuite{
		common.BootlooseSuite{
			ControllerCount: 1,
			WorkerCount:     2,
		},
	}
	suite.Run(t, &s)
}
