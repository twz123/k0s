locals {
  # k8s_api_port             = 6443
  # k0s_api_port             = 9443
  # konnectivity_server_port = 8132

  k0sctl_config = {
    apiVersion = "k0sctl.k0sproject.io/v1beta1"
    kind       = "Cluster"
    metadata   = { name = "k0s-cluster" }
    spec = {
      k0s = merge(
        var.k0s_version == null ? {} : {
          version = var.k0s_version
        },
        {
          version       = var.k0s_version
          dynamicConfig = var.k0s_dynamic_config
          config = { spec = merge(
            { telemetry = { enabled = false, }, },
            # (var.loadbalancer != null ? { api = {
            #   externalAddress = module.loadbalancer.info.ipv4,
            #   sans = [
            #     module.loadbalancer.info.name,
            #     module.loadbalancer.info.ipv4,
            #   ],
            # }, } : {}),
            { for k, v in var.k0s_config_spec : k => v if v != null }
          ), }
        },
      )

      hosts = [for host in var.hosts : merge(
        {
          role = host.role
          ssh = {
            address = host.ipv4
            keyPath = var.ssh_private_key_filename
            port    = 22
            user    = var.ssh_username
          }
          installFlags = concat(
            var.k0s_install_flags,
            contains(["controller", "controller+worker"], host.role) ? var.k0s_controller_install_flags : [],
            contains(["worker", "controller+worker"], host.role) ? var.k0s_worker_install_flags : [],
          )
          uploadBinary = var.k0s_executable_path != null
        },

        # host.hooks == null ? {} : merge({
        #   hooks = host.hooks.apply == null ? {} : merge({
        #     apply = host.hooks.apply.before == null ? {} : {
        #       before = host.hooks.apply.before
        #     }
        #   })
        # })

        var.k0s_executable_path == null ? {} : {
          k0sBinaryPath = var.k0s_executable_path
        },

        # var.k0sctl_airgap_image_bundle != null && contains(["worker", "controller+worker"], host.role) ? {
        #   files = [
        #     {
        #       src    = var.k0sctl_airgap_image_bundle
        #       dstDir = "/var/lib/k0s/images/"
        #       name   = "bundle-file"
        #       perm   = "0755"
        #     }
        #   ]
        # } : {},
      )]
    }
  }
}
