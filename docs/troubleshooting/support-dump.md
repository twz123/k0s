# Support Insight

In many cases, especially when looking for [commercial support](../commercial-support.md) there's a need for share the cluster state with other people.
While one could always give access to the live cluster that is not always desired nor even possible.

In these cases, use the work provided by [troubleshoot.sh](https://troubleshoot.sh).

The troubleshoot tool can produce a dump of the cluster state for sharing. The [sbctl](https://github.com/replicatedhq/sbctl) tool can expose the dump tarball as a Kubernetes API.

The following example shows how this works with k0s.

## Setting up

To gather all the needed data, install another tool called [`support-bundle`](https://troubleshoot.sh/docs/support-bundle/introduction/).

Download it from the [releases page](https://github.com/replicatedhq/troubleshoot/releases). Ensure the correct architecture is selected.

## Creating support bundle

A Support Bundle needs to know what to collect and optionally, what to analyze. This is defined in a YAML file.

While data collection can be customized, the reference configuration for k0s covers core elements such as:

- collecting info on the host
- collecting system component statuses from `kube-system` namespace
- checking health of Kubernetes API, Etcd etc. components
- collecting k0s logs
- checking status of firewalls, anti-virus etc. services which are known to interfere with Kubernetes

Because host-level information is required, run the commands directly on controllers and workers.

After setting up the [tooling](#setting-up), run the following command to generate a support bundle:

```shell
support-bundle --kubeconfig /var/lib/k0s/pki/admin.conf https://docs.k0sproject.io/stable/support-bundle-<role>.yaml
```

Above `<role>` refers to either `controller` or `worker`. Different roles require different information. When running a controller with `--enable-worker` or `--single`, which also makes it a worker, capture a combined dump:

```shell
support-bundle --kubeconfig /var/lib/k0s/pki/admin.conf https://docs.k0sproject.io/stable/support-bundle-controller.yaml https://docs.k0sproject.io/stable/support-bundle-worker.yaml
```

After data collection and analysis complete, a file named `support-bundle-<timestamp>.tar.gz` appears. Share this file as needed.
