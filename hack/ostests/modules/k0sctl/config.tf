locals {
  k8s_api_port             = 6443
  k0s_api_port             = 9443
  konnectivity_server_port = 8132

  hosts = concat(
    [for host in module.controllers.*.info :
      merge(host, {
        controller_enabled = true
        worker_enabled     = var.controller_k0s_enable_worker
      })
    ],
    [for host in module.workers.*.info :
      merge(host, {
        controller_enabled = false
        worker_enabled     = true
      })
    ],
  )

  k0sctl_config = {
    apiVersion = "k0sctl.k0sproject.io/v1beta1"
    kind       = "Cluster"
    metadata   = { name = "k0s-cluster" }
    spec = {
      k0s = {
        version       = var.k0s_version
        dynamicConfig = var.k0s_dynamic_config
        config = { spec = merge(
          { telemetry = { enabled = false, }, },
          (var.loadbalancer != null ? { api = {
            externalAddress = module.loadbalancer.info.ipv4,
            sans = [
              module.loadbalancer.info.name,
              module.loadbalancer.info.ipv4,
            ],
          }, } : {}),
          { for k, v in var.k0s_config_spec : k => v if v != null }
        ), }
      }
      hosts = [for host in local.hosts : merge(
        {
          role = host.controller_enabled ? (host.worker_enabled ? "controller+worker" : "controller") : "worker"
          ssh = {
            address = host.ipv4
            keyPath = local_file.ssh_private_key.filename
            port    = 22
            user    = var.host_user
          }
          installFlags = concat(
            var.k0sctl_k0s_install_flags,
            host.controller_enabled ? var.k0sctl_k0s_controller_install_flags : [],
            host.worker_enabled ? var.k0sctl_k0s_worker_install_flags : [],
          )
          uploadBinary = true
        },
        var.k0sctl_k0s_binary == null ? {} : {
          k0sBinaryPath = var.k0sctl_k0s_binary
        },
        host.worker_enabled && var.k0sctl_airgap_image_bundle != null ? {
          files = [
            {
              src    = var.k0sctl_airgap_image_bundle
              dstDir = "/var/lib/k0s/images/"
              name   = "bundle-file"
              perm   = "0755"
            }
          ]
        } : {},
      )]
    }
  }
}
