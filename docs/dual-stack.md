<!--
SPDX-FileCopyrightText: 2021 k0s authors

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# IPv4/IPv6 dual-stack networking

Enabling dual-stack networking in k0s allows your cluster to handle both IPv4 and
IPv6 addresses. Follow the configuration examples below to set up dual-stack mode.

## Enabling dual-stack using the default CNI (kube-router)

In order to enable dual-stack networking using the default CNI provider, use the
following example configuration:

```yaml
spec:
  network:
    # kube-router is the default CNI provider
    # provider: kube-router
    podCIDR: 10.244.0.0/16
    serviceCIDR: 10.96.0.0/12
    dualStack:
      enabled: true
      IPv6podCIDR: fd00::/108
      IPv6serviceCIDR: fd01::/108
```

This configuration will set up all Kubernetes components and kube-router
accordingly for dual-stack networking.

## Using Calico as the CNI provider

Calico does not support IPv6 tunneling in the default `vxlan` mode, so if you
prefer to use Calico as your CNI provider, make sure to select `bird` mode. Use
the following example configuration:

```yaml
spec:
  network:
    provider: calico
    calico:
      mode: bird
    podCIDR: 10.244.0.0/16
    serviceCIDR: 10.96.0.0/12
    dualStack:
      enabled: true
      IPv6podCIDR: fd00::/108
      IPv6serviceCIDR: fd01::/108
```

## Specifying the default IP family

In Kubernetes dual stack clusters, by default all the services are single stack,
including `kubernetes.default.svc`, which is used to communicate with the kube-servers.

This is specially importantwhen specifying explicitly `spec.api.externalAddress` or
`spec.api.address`.

To explicitly define the family which will be used by default use the following
configuration:

```yaml
spec:
  network:
    # primaryAddressFamily is optional
    priamryAddressFamily: <IPv4|IPv6>
```

If not defined explicitly, k0s will determine it based on `spec.api.externalAddress`,
if this field is not defined, k0s will use `spec.api.address`. If the field used is
a hostname or both are empty, k0s will use IPv4.

## Custom CNI providers

While the dual-stack configuration section configures all components managed by
k0s for dual-stack operation, the custom CNI provider must also be configured
accordingly. Refer to the documentation for your specific CNI provider to ensure
a proper dual-stack setup that matches that of k0s.

## Additional Resources

For more detailed information and troubleshooting, refer to the following resources:

* <https://kubernetes.io/docs/concepts/services-networking/dual-stack/>
* <https://kubernetes.io/docs/tasks/network/validate-dual-stack/>
* <https://www.tigera.io/blog/dual-stack-operation-with-calico-on-kubernetes/>
* <https://docs.tigera.io/calico/3.27/networking/ipam/ipv6#enable-dual-stack>
