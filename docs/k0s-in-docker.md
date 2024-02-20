# Run k0s in Docker

You can create a k0s cluster on top of Docker.

## Prerequisites

You will require a [Docker environment](https://docs.docker.com/get-docker/)
running on a Mac, Windows, or Linux system.

## Container images

The k0s OCI images are published both on Docker Hub and GitHub. For reasons of
simplicity, the examples given here use Docker Hub (GitHub requires a separate
authentication that is not covered). Alternative links include:

- `docker.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0`
- `ghcr.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0`

**Note:** Due to Docker's tag validation scheme, we have to use `-` as the k0s
version separator instead of the usual `+`. So for example k0s version
`v{{{extra.k8s_version}}}+k0s.0` is tagged as
`docker.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0`.

## Start k0s

### 1. Run a controller

By default, running the k0s Docker image will launch a controller with workloads
enabled (i.e. a controller with the `--enable-worker` flag) to provide an easy
local testing "cluster":

```sh
docker run -d --name k0s-controller --hostname k0s-controller \
  -v /var/lib/k0s -v /var/log/pods `# this is where k0s stores its data` \
  --tmpfs /run `# this is where k0s stores runtime data` \
  --privileged `# this is the easiest way to enable container-in-container workloads` \
  -p 6443:6443 `# publish the Kubernetes API server port` \
  docker.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0
```

Alternatively, run a controller only:

```sh
docker run -d --name k0s-controller --hostname k0s-controller \
  -v /var/lib/k0s `# this is where k0s stores its data` \
  --tmpfs /run `# this is where k0s stores runtime data` \
  -p 6443:6443 `# publish the Kubernetes API server port` \
  docker.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0 \
  k0s controller
```

### 2. (Optional) Add additional workers

You can attach multiple workers nodes into the cluster to then distribute your
application containers to separate workers.

For each required worker:

1. Acquire a join token for the worker:

   ```sh
   token=$(docker exec k0s `# or k0s-controller` k0s token create --role=worker)
   ```

2. Run the container to create and join the new worker:

   ```sh
   docker run -d --name k0s-worker1 --hostname k0s-worker1 \
     -v /var/lib/k0s -v /var/log/pods `# this is where k0s stores its data` \
     --tmpfs /run `# this is where k0s stores runtime data` \
     --privileged `# this is the easiest way to enable container-in-container workloads` \
     docker.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0 \
     k0s worker $token
   ```

   Alternatively, with fine-grained permissions:

   ```sh
   docker run -d --name k0s-worker1 --hostname k0s-worker1 \
     -v /var/lib/k0s -v /var/log/pods `# this is where k0s stores its data` \
     --tmpfs /run `# this is where k0s stores runtime data` \
     -v /dev/kmsg:/dev/kmsg:ro --device-cgroup-rule='c 1:11 r' `# allow reading /dev/kmsg (check device type via "stat -c %Hr:%Lr /dev/kmsg")` \
     --cap-add sys_admin --cap-add net_admin \
     --cap-add sys_ptrace `# required: RunContainerError (figure out exact error)` \
     --cap-add sys_resource `# runc create failed: unable to start container process: can't get final child's PID from pipe: EOF: unknown` \
     --security-opt seccomp=unconfined `# required for runc to access the session keyring` \
     docker.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0 \
     k0s worker "$token"
   ```

   Note that, depending on your cluster configuration and workloads, more
   permissions are required.

### 3. Access your cluster

Access your cluster using kubectl:

```sh
docker exec k0s-controller k0s kubectl get nodes
```

Alternatively, grab the kubeconfig file with `docker exec k0s-controller k0s
kubeconfig admin` and paste it into [Lens](https://k8slens.dev/).

## Use Docker Compose (alternative)

As an alternative you can run k0s using Docker Compose:

```yaml
version: "3.9"
services:
  k0s:
    container_name: k0s
    image: docker.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0
    hostname: k0s
    volumes: # this is where k0s stores its data
      - /var/lib/k0s
      - /var/log/pods
    tmpfs:
      - /run # this is where k0s stores runtime data
    ports:
      - 6443:6443 # publish the Kubernetes API server port
    privileged: true # this is the easiest way to enable container-in-container workloads
    network_mode: bridge # other modes are unsupported, see below
    environment:
      K0S_CONFIG: |-
        apiVersion: k0s.k0sproject.io/v1beta1
        kind: ClusterConfig
        metadata:
          name: k0s
        # Any additional configuration goes here ...
```

## Known limitations

### No custom Docker networks

Currently, k0s nodes cannot be run if the containers are configured to use
custom networks (for example, with `--net my-net`). This is because Docker sets
up a custom DNS service within the network which creates issues with CoreDNS. No
completely reliable workarounds are available, however no issues should arise
from running k0s cluster(s) on a bridge network.

## Next Steps

- [Install using k0sctl](k0sctl-install.md): Deploy multi-node clusters using just one command
- [Control plane configuration options](configuration.md): Networking and datastore configuration
- [Worker node configuration options](worker-node-config.md): Node labels and kubelet arguments
- [Support for cloud providers](cloud-providers.md): Load balancer or storage configuration
- [Installing the Traefik Ingress Controller](examples/traefik-ingress.md): Ingress deployment information
