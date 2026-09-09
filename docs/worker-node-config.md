<!--
SPDX-FileCopyrightText: 2021 k0s authors
SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Configuration options for worker nodes

Although the `k0s worker` command does not take in any special YAML configuration, there are still methods for configuring the workers to run various components.

## Node labels

The `k0s worker` command accepts the `--labels` flag, with which you can make the newly joined worker node register itself, in the Kubernetes API, with the given set of labels.

For example, running the worker with `k0s worker --token-file k0s.token --labels="k0sproject.io/foo=bar,k0sproject.io/other=xyz"` results in:

{% set kubelet_ver = k8s_version + '+k0s' -%}
{% set kubelet_ver_len = kubelet_ver | length -%}

```console
$ kubectl get node --show-labels
NAME      STATUS     ROLES    AGE   {{{ 'VERSION'   | ljust(kubelet_ver_len) }}}   LABELS
worker0   NotReady   <none>   10s   {{{ kubelet_ver | ljust(kubelet_ver_len) }}}   beta.kubernetes.io/arch=amd64,beta.kubernetes.io/os=linux,k0sproject.io/foo=bar,k0sproject.io/other=xyz,kubernetes.io/arch=amd64,kubernetes.io/hostname=worker0,kubernetes.io/os=linux
```

Controller worker nodes are assigned `node.k0sproject.io/role=control-plane` and `node-role.kubernetes.io/control-plane=true` labels:

```console
$ kubectl get node --show-labels
NAME          STATUS     ROLES           AGE   {{{ 'VERSION'   | ljust(kubelet_ver_len) }}}   LABELS
controller0   NotReady   control-plane   10s   {{{ kubelet_ver | ljust(kubelet_ver_len) }}}   beta.kubernetes.io/arch=amd64,beta.kubernetes.io/os=linux,kubernetes.io/hostname=worker0,kubernetes.io/os=linux,node.k0sproject.io/role=control-plane,node-role.kubernetes.io/control-plane=true
```

**Note:** Setting the labels is only effective on the first registration of the node. Changing the labels thereafter has no effect.

## Taints

The `k0s worker` command accepts the `--taints` flag, with which you can make the newly joined worker node register itself with the given set of taints.

**Note:** Controller nodes running with `--enable-worker` are assigned `node-role.kubernetes.io/control-plane:NoExecute` taint automatically. You can disable default taints using `--no-taints` parameter.

```shell
kubectl get nodes -o custom-columns=NAME:.metadata.name,TAINTS:.spec.taints
```

```shell
NAME          TAINTS
controller0   [map[effect:NoSchedule key:node-role.kubernetes.io/control-plane]]
worker0       <none>
```

## Kubelet configuration

The `k0s worker` command accepts a generic flag to pass in any set of arguments
for the kubelet process.

For example, running `k0s worker --token-file=k0s.token
--kubelet-extra-args="--node-ip=1.2.3.4 --address=0.0.0.0"` passes in the given
flags to Kubelet as-is. As such, you must confirm that any flags you are passing
in are properly formatted and valued as k0s will not validate those flags.

### Worker Profiles

Kubelet configuration fields can also be set via worker profiles. Worker
profiles are defined in the main k0s.yaml and are used to generate ConfigMaps
containing a custom `kubelet.config.k8s.io/v1beta1/KubeletConfiguration`
object.
See also the [examples of k0s.yaml containing worker
profiles](./configuration.md#configuration-examples) and the [list of possible
Kubelet configuration
fields](https://kubernetes.io/docs/reference/config-api/kubelet-config.v1beta1/).

### Kubelet serving certificates

By default, k0s configures kubelets to request their serving certificates from
the Kubernetes API via [Certificate Signing Requests] (CSRs), by enabling
`serverTLSBootstrap` in the generated kubelet configuration. The kubelet then
requests a certificate from the `kubernetes.io/kubelet-serving` signer and uses
it to serve its own API. k0s issues these certificates from a dedicated
[kubelet-serving CA](#kubelet-serving-ca). Clients of the kubelet API verify the
certificate against that CA. The Kubernetes API server does so, for example,
when serving `kubectl logs` and `kubectl exec` requests. So does k0s's
`metrics-server` component, which bundles the [Kubernetes Metrics Server], when
scraping the kubelet's resource metrics.

Kubernetes doesn't approve these CSRs automatically. k0s ships a controller
component called `csr-approver` that does so, provided the request meets all of
the following conditions:

- The PEM-encoded certificate request size doesn't exceed 512 KiB.
- It is a well-formed kubelet serving certificate request, according to
  Kubernetes' validation rules.
- The certificate request has no more than 4096 SANs, counting DNS names and IP
  addresses together.
- It was created by the very node it requests the certificate for. The
  requesting user must have the `system:node:<nodeName>` name and be a member of
  the `system:nodes` group, and the certificate's common name must be identical
  to the requesting user name.
- The requested DNS names and IP addresses don't identify the cluster's control
  plane or other in-cluster services: no IP address may be inside the service
  CIDR(s), and no DNS name may be `kubernetes`, `kubernetes.default`, or a name
  within the `svc` or cluster domains.
- The node exists in the cluster.
- The node's `status.addresses` contains no more than 4096 entries.
- The requested DNS names and IP addresses are all listed in the node's
  `status.addresses`.

Requests that don't meet these conditions are left pending, and the reason is
logged by the k0s controller. Note that a node's addresses are reported by its
kubelet, so flags like `--node-ip` directly influence which addresses a
certificate may be issued for.

The CSR approver checks for pending requests periodically. It only considers the
newest pending request of each node per pass, so that a node can't hold up
others by creating large numbers of requests. It can be turned off via `k0s
controller --disable-components csr-approver`. In that case, kubelet serving
certificates need to be approved by some other means, otherwise the affected
kubelet APIs remain unavailable.

[Certificate Signing Requests]: https://kubernetes.io/docs/reference/access-authn-authz/certificate-signing-requests/
[Kubernetes Metrics Server]: https://github.com/kubernetes-sigs/metrics-server

### Kubelet-serving CA

k0s issues kubelet serving certificates from a certificate authority that is
dedicated to the `kubernetes.io/kubelet-serving` signer: the kubelet-serving CA.
This way, a kubelet serving certificate is trusted by exactly those clients that
connect to kubelets, and by nothing else. In particular, workloads that trust
the cluster CA in order to verify the Kubernetes API server don't trust kubelet
serving certificates.

The kubelet-serving CA is derived from the cluster CA. Every controller computes
the same CA on its own whenever it starts, so there's nothing to distribute or
to persist, and the CA follows the cluster CA whenever that one is [replaced].
Its files are written to the run directory, `/run/k0s` by default:

- `kubelet-serving-ca.crt` and `kubelet-serving-ca.key`: the CA certificate and
  its private key.
- `kubelet-serving-ca-bundle.crt`: the trust bundle for kubelet serving
  certificates. It holds the kubelet-serving CA, followed by the cluster CA, so
  that kubelet serving certificates issued by the cluster CA remain trusted.

Clients that verify kubelet serving certificates, such as monitoring systems
that scrape kubelets directly, need to trust this bundle rather than the service
account CA. k0s publishes it in the cluster:

- As the `kubelet-serving-ca.crt` ConfigMap in the `kube-system` namespace,
  under the `ca.crt` key, just like the `kube-root-ca.crt` ConfigMap that holds
  the cluster CA. Copy it into the namespaces that need it.
- On Kubernetes 1.37 and newer, both for the control plane and the kubelet, as
  the `kubernetes.io:kubelet-serving:k0s` [ClusterTrustBundle]. It can be
  mounted in any namespace via a projected volume:

  ```yaml
  volumes:
    - name: kubelet-serving-ca
      projected:
        sources:
          - clusterTrustBundle:
              signerName: kubernetes.io/kubelet-serving
              labelSelector: {}
              path: ca.crt
  ```

Workloads that verify kubelet serving certificates against the cluster CA stop
working once kubelets get certificates issued by the kubelet-serving CA. To keep
them working while they are switched over to the trust bundle, disable the
`kubelet-serving-ca` component on all controllers, e.g. via
`--disable-components=kubelet-serving-ca`. Kubelet serving certificates are then
issued by the cluster CA, as before. The trust bundle is published and trusted
either way, so the component can be enabled again at any time.

[replaced]: troubleshooting/certificate-authorities.md#replacing-the-kubernetes-ca-and-sa-key-pair
[ClusterTrustBundle]: https://kubernetes.io/docs/reference/access-authn-authz/certificate-signing-requests/#cluster-trust-bundles

## IPTables Mode

k0s detects the iptables backend automatically based on the existing records. On a brand-new setup, `iptables-nft` will be used.
There is an `--iptables-mode` flag to specify the mode explicitly. Valid values: `nft`, `legacy` and `auto` (default).

```shell
k0s worker --iptables-mode=nft
```
