# Run k0s in Docker

You can create a k0s cluster on top of Docker.

## Prerequisites

You will require a [Docker environment](https://docs.docker.com/get-docker/) running on a Mac, Windows, or Linux system.

## Container images

The k0s OCI images are published to both Docker Hub and GitHub Container
registry. For simplicity, the examples given here use Docker Hub (GitHub
requires separate authentication, which is not covered here). The image names
are as follows:

- `docker.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0`
- `ghcr.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0`

**Note:** Due to Docker's tag validation scheme, `-` is used as the k0s version
separator instead of the usual `+`. For example, k0s version
`v{{{extra.k8s_version}}}+k0s.0` is tagged as
`docker.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0`.

## Start k0s

### 1. Run a controller

By default, running the k0s OCI image will launch a controller with workloads
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

Explanation of command line arguments:

- `-d` runs the container in detached mode, i.e. in the background.
- `--name k0s-controller` names the container "k0s-controller".
- `--hostname k0s-controller` sets the hostname of the container to
  "k0s-controller".
- `-v /var/lib/k0s -v /var/log/pods` creates two Docker volumes and mounts them
  to `/var/lib/k0s` and `/var/log/pods` respecively inside the container,
  ensuring that cluster data persists across container restarts.
- `--tmpfs /run` FIXME
- `--privileged` gives the container the elevated privileges that k0s needs to
  function properly within Docker.
- `-p 6443:6443` exposes the container's Kubernetes API server port 6443 to the
  host, allowing you to interact with the cluster externally.
- `docker.io/k0sproject/k0s:v{{{ extra.k8s_version }}}-k0s.0` is the name of the
  k0s Docker image to run.
- `--` marks the end of the argument processing for the docker binary.
- `k0s controller --enable-worker` is the actual command to run inside the
  Docker container. It starts the k0s controller and enables a worker node
  within the same container, creating a single node cluster.

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

You can attach multiple workers nodes into the cluster to then distribute your application containers to separate workers.

For each required worker:

1. Acquire a join token for the worker:

   ```sh
   token=$(docker exec k0s-controller k0s token create --role=worker)
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
     --security-opt seccomp=unconfined \
     -v /dev/kmsg:/dev/kmsg:ro --device-cgroup-rule='c 1:11 r' \
     --cap-add sys_admin --cap-add net_admin \
     --cap-add sys_ptrace \
     --cap-add sys_resource \
     docker.io/k0sproject/k0s:v{{{extra.k8s_version}}}-k0s.0 \
     k0s worker "$token"
   ```

  <!-- markdownlint-disable MD007 https://github.com/DavidAnson/markdownlint/issues/973 -->

  - `-v /dev/kmsg:/dev/kmsg:ro --device-cgroup-rule='c 1:11 r'` allows reading
    /dev/kmsg from inside the container
    <!-- check device type via "check device type via `stat -c %Hr:%Lr /dev/kmsg` -->
  - `--security-opt seccomp=unconfined` is required for runc to access the
    session keyring

  Capabilities explained:

  - `CAP_SYS_ADMIN` is required for a multitude of administrative tasks, like
    mounting and unmouning.
  - `CAP_NET_ADMIN`
  - `CAP_SYS_PTRACE`: RunContainerError (figure out exact error)
  - `CAP_SYS_RESOURCE` # runc create failed: unable to start container process:
    can't get final child's PID from pipe: EOF: unknown` \

   Note that more privileges may be required depending on your cluster
   configuration and workloads.

Repeat these steps for each additional worker node needed. Ensure that workers can reach the controller on port 6443.

### 3. Access your cluster

#### a) Using kubectl within the container

To check cluster status and list nodes, use:

```sh
docker exec k0s-controller k0s kubectl get nodes
```

#### b) Using kubectl locally

To configure local access to your k0s cluster, follow these steps:

1. Generate the kubeconfig:

    ```sh
    docker exec k0s-controller k0s kubeconfig admin > ~/.kube/k0s.config
    ```

2. Update kubeconfig with Localhost Access:

    To automatically replace the server IP with localhost dynamically in `~/.kube/k0s.config`, use the following command:

    ```sh
    sed -i '' -e "$(awk '/server:/ {print NR; exit}' ~/.kube/k0s.config)s|https://.*:6443|https://localhost:6443|" ~/.kube/k0s.config
    ```

    This command updates the kubeconfig to point to localhost, allowing access to the API server from your host machine

3. Set the KUBECONFIG Environment Variable:

    ```sh
    export KUBECONFIG=~/.kube/k0s.config
    ```

4. Verify Cluster Access:

    ```sh
    kubectl get nodes
    ```

#### c) Use [Lens]

Access the k0s cluster using Lens by following the instructions on [how to add a
cluster].

[Lens]: https://k8slens.dev/
[how to add a cluster]: https://docs.k8slens.dev/getting-started/add-cluster/

## Use Docker Compose (alternative)

As an alternative you can run k0s using Docker Compose:

```yaml
services:
  k0s:
    image: k0s-test
    container_name: k0s
    hostname: k0s
    read_only: true # k0s won't write any data outside the below paths
    volumes: # this is where k0s stores its data
      - /var/lib/k0s
      - /var/log/pods
    tmpfs:
      - /run # this is where k0s stores runtime data
    ports:
      - 6443:6443 # publish the Kubernetes API server port
    privileged: true # this is the easiest way to enable container-in-container workloads
    network_mode: bridge # other modes are unsupported, see below
    configs:
      - source: k0s.yaml
        target: /etc/k0s/k0s.yaml

configs:
  k0s.yaml:
    content: |
      apiVersion: k0s.k0sproject.io/v1beta1
      kind: ClusterConfig
      metadata:
        name: k0s
      spec:
        storage:
          type: kine
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
