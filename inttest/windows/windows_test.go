// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package windows

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/k0sproject/k0s/inttest/common"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type WindowsSuite struct {
	suite.Suite
	kc         *kubernetes.Clientset
	restConfig *rest.Config
}

func TestWindowsSuite(t *testing.T) {
	suite.Run(t, new(WindowsSuite))
}

func (s *WindowsSuite) SetupSuite() {
	// kubeconfig := "/Users/jnummelin/.kube/win-config"
	kubeconfig := os.Getenv("KUBECONFIG")
	s.Require().NotEmpty(kubeconfig, "KUBECONFIG must be set for this test to work")
	s.T().Logf("Using kubeconfig: %s", kubeconfig)
	var err error
	s.restConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	s.Require().NoError(err, "Failed to build kubeconfig")
	s.kc, err = kubernetes.NewForConfig(s.restConfig)
	s.Require().NoError(err, "Failed to create Kubernetes client")

	// Get the server address and try to connect to it via go std lib
	server := s.restConfig.Host
	s.T().Logf("Trying to connect to Kube API server: %s", server)
	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	// Test the connection via stdlib client
	resp, err := client.Get(fmt.Sprintf("%s/version", server))
	if err != nil {
		s.T().Logf("Failed to connect to Kube API server: %v", err)
	} else {
		s.T().Logf("Successfully connected to Kube API server, got status: %s", resp.Status)
	}
	defer resp.Body.Close()

	// Test the connection using os.Exec for kubectl
	cmd := exec.Command("kubectl", "version")
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		s.T().Logf("Failed to connect to Kube API server via kubectl: %v", err)
	} else {
		s.T().Logf("Successfully connected to Kube API server via kubectl, got output: %s", out.String())
	}
}

func (s *WindowsSuite) TearDownSuite() {
	s.T().Log("Cleaning up test resources")
	// Delete nginx and iis pods and svcs
	err := s.kc.CoreV1().Pods("default").Delete(context.Background(), "iis", metav1.DeleteOptions{})
	s.T().Log("Deleted IIS pods, error:", err)

	err = s.kc.CoreV1().Pods("default").Delete(context.Background(), "nginx-linux", metav1.DeleteOptions{})
	s.T().Log("Deleted Nginx pods, error:", err)

	err = s.kc.CoreV1().Services("default").Delete(context.Background(), "iis-windows-svc", metav1.DeleteOptions{})
	s.T().Log("Deleted IIS service, error:", err)
	err = s.kc.CoreV1().Services("default").Delete(context.Background(), "nginx-linux-svc", metav1.DeleteOptions{})
	s.T().Log("Deleted Nginx service, error:", err)
}

// This test work on an existing cluster where there's 1 or more Windows nodes
// We test few things:
// - Node readiness
// - Pod scheduling; check that all expected components are running (mainly Calico & kube-proxy)
// - Pod-to-pod networking works across Windows and Linux nodes
func (s *WindowsSuite) TestWindows() {

	ctx := s.T().Context()

	// Wait for all nodes to be ready
	s.T().Log("Waiting for all nodes to be ready")
	require.NoError(s.T(), common.Poll(ctx, func(ctx context.Context) (bool, error) {
		nodes, err := s.kc.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		if len(nodes.Items) < 2 {
			s.T().Logf("Waiting for at least 2 nodes, got %d", len(nodes.Items))
			return false, nil
		}

		for _, node := range nodes.Items {
			// Find the ready condition
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady {
					s.T().Logf("Node %s condition %s is %s", node.Name, condition.Type, condition.Status)
					if condition.Status != corev1.ConditionTrue {
						return false, nil
					}
				}
			}
		}
		return true, nil
	}))
	s.T().Log("All nodes are ready")

	// Wait for system services to boot up
	s.T().Log("Waiting for system DaemonSets to be ready")
	s.T().Log("Waiting for kube-proxy DaemonSet to be ready")
	require.NoError(s.T(), common.WaitForDaemonSet(ctx, s.kc, "kube-proxy", "kube-system"))
	s.T().Log("Waiting for kube-proxy-windows DaemonSet to be ready")
	require.NoError(s.T(), common.WaitForDaemonSet(ctx, s.kc, "kube-proxy-windows", "kube-system"))
	s.T().Log("Waiting for calico-node DaemonSet to be ready")
	require.NoError(s.T(), common.WaitForDaemonSet(ctx, s.kc, "calico-node", "kube-system"))
	s.T().Log("Waiting for calico-node-windows DaemonSet to be ready")
	require.NoError(s.T(), common.WaitForDaemonSet(ctx, s.kc, "calico-node-windows", "kube-system"))

	// Schedule a test pod on each side
	// Windows

	s.Require().NoError(runWindowsDeployment(ctx, s.kc))

	s.Require().NoError(common.WaitForPod(ctx, s.kc, "iis", "default"))
	// Linux
	s.Require().NoError(runLinuxDeployment(ctx, s.kc))
	s.Require().NoError(common.WaitForPod(ctx, s.kc, "nginx-linux", "default"))

	winSvcIP, err := svcIP(ctx, s.kc, "iis-windows-svc")
	s.Require().NoError(err)
	linuxSvcIP, err := svcIP(ctx, s.kc, "nginx-linux-svc")
	s.Require().NoError(err)

	// Windows --> Linux connectivity
	pwsh := fmt.Sprintf(`(Invoke-WebRequest -UseBasicParsing -Uri http://%s).StatusCode`, linuxSvcIP)
	out, err := common.PodExecPowerShell(s.kc, s.restConfig, "iis", "default", pwsh)
	s.Require().NoError(err)
	s.T().Logf("Response from Linux service: %s", out)
	s.Require().Equal("200", strings.TrimSpace(out))

	// Linux --> Windows connectivity
	curl := fmt.Sprintf(`curl -s -o /dev/null -w "%%{http_code}" http://%s`, winSvcIP)
	out, err = common.PodExecShell(s.kc, s.restConfig, "nginx-linux", "default", curl)
	s.Require().NoError(err)
	s.T().Logf("Response from Windows service: %s", out)
	s.Require().Equal("200", strings.TrimSpace(out))
}

func svcIP(ctx context.Context, kc *kubernetes.Clientset, svcName string) (string, error) {
	svc, err := kc.CoreV1().Services("default").Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return svc.Spec.ClusterIP, nil
}

func runLinuxDeployment(ctx context.Context, kc *kubernetes.Clientset) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-linux",
			Namespace: "default",
			Labels:    map[string]string{"app": "nginx-linux"},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{"kubernetes.io/os": "linux"},
			Containers: []corev1.Container{
				{
					Name:  "nginx",
					Image: "nginx:stable",
					Ports: []corev1.ContainerPort{
						{
							ContainerPort: 80,
						},
					},
				},
			},
		},
	}
	_, err := kc.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-linux-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "nginx-linux"},
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(80),
				},
			},
		},
	}
	_, err = kc.CoreV1().Services("default").Create(ctx, svc, metav1.CreateOptions{})
	return err
}

func runWindowsDeployment(ctx context.Context, kc *kubernetes.Clientset) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "iis",
			Namespace: "default",
			Labels:    map[string]string{"app": "iis-windows"},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{"kubernetes.io/os": "windows"},
			Containers: []corev1.Container{
				{
					Name:  "iis",
					Image: "mcr.microsoft.com/windows/servercore/iis",
				},
			},
		},
	}
	_, err := kc.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "iis-windows-svc",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": "iis-windows"},
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(80),
				},
			},
		},
	}
	_, err = kc.CoreV1().Services("default").Create(ctx, svc, metav1.CreateOptions{})
	return err
}
