package konnectivity

import (
	"context"
	"fmt"

	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/component/worker"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

type Agent struct {
	StaticPods worker.StaticPods

	agentPod worker.StaticPod
}

var _ manager.Component = (*Agent)(nil)

// Init implements manager.Component.
func (a *Agent) Init(context.Context) error {
	agentPod, err := a.StaticPods.ClaimStaticPod("kube-system", "konnectivity-agent")
	if err != nil {
		return fmt.Errorf("failed to claim static pod for konnectivity agent: %w", err)
	}

	a.agentPod = agentPod
	return nil
}

func (a *Agent) Start(context.Context) error {
	a.agentPod.SetManifest()

}

func (a *Agent) Stop() error {
	agentPod := a.agentPod
	a.agentPod = nil
	if agentPod != nil {
		agentPod.Drop()
	}

	panic("unimplemented")
}

type konnectivityAgentConfig struct {
	proxyServerHost string
	proxyServerPort uint16
	agentPort       uint16
	image           string
	serverCount     uint
	pullPolicy      corev1.PullPolicy
	hostNetwork     bool
}

func (c *konnectivityAgentConfig) makePodManifest() corev1.Pod {
	return corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "konnectivity-agent",
			Namespace: metav1.NamespaceSystem,
			Labels:    applier.CommonLabels("konnectivity-agent"),
		},
		Spec: corev1.PodSpec{
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:       ptr.To(true),
				SupplementalGroups: []int64{0}, // in order to read the projected service account token
			},
			HostNetwork: c.hostNetwork,
			Containers: []corev1.Container{{
				Name:            "konnecitvity-agent",
				Image:           c.image,
				ImagePullPolicy: c.pullPolicy,
				Env: []corev1.EnvVar{
					{
						Name: "NODE_IP",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								FieldPath: "status.hostIP",
							},
						},
					},
				},
				Args: []string{
					"--logtostderr=true",
					"--ca-cert=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
					fmt.Sprintf("--proxy-server-host=%d", c.proxyServerHost),
					fmt.Sprintf("--proxy-server-port=%d", c.proxyServerPort),
					"--service-account-token-path=/var/run/secrets/tokens/konnectivity-agent-token",
					"--agent-identifiers=host=$(NODE_IP)",
					"--agent-id=$(NODE_IP)",
				},
				SecurityContext: &corev1.SecurityContext{
					ReadOnlyRootFilesystem:   ptr.To(true),
					Privileged:               ptr.To(false),
					AllowPrivilegeEscalation: ptr.To(false),
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{"ALL"},
					},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "konnectivity-agent-token",
					MountPath: "/var/run/secrets/tokens",
					ReadOnly:  true,
				}},
				LivenessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Port: intstr.FromInt(8093),
							Path: "/healthz",
						},
					},
					InitialDelaySeconds: 15,
					TimeoutSeconds:      15,
				},
				// Helps to quickly identify pods with connection issues.
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Port: intstr.FromInt(8093),
							Path: "/readyz",
						},
					},
					InitialDelaySeconds: 15,
					TimeoutSeconds:      15,
				},
			}},
			ServiceAccountName: "konnectivity-agent",
			Volumes: []corev1.Volume{{
				Name: "konnectivity-agent-token",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{
							corev1.VolumeProjection{
								ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
									Path:     "konnectivity-agent-token",
									Audience: "system:konnectiity-server",
								},
							},
						},
					},
				}},
			},
		},
	}
}
