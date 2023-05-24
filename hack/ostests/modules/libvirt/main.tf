provider "libvirt" {
  uri = var.libvirt_provider_uri
}

locals {
  resource_pool_name = "${var.resource_name_prefix}resource-pool"
}

resource "tls_private_key" "ssh" {
  algorithm   = "ECDSA"
  ecdsa_curve = "P384"
}

# resource "local_file" "ssh_private_key" {
#   content         = tls_private_key.ssh.private_key_pem
#   filename        = "${var.profile_folder}/ssh-private-key.pem"
#   file_permission = "0400"
# }

# Creates a resource pool for virtual machine volumes
resource "libvirt_pool" "resource_pool" {
  name = local.resource_pool_name

  type = "dir"
  path = pathexpand("${trimsuffix(var.resource_pool_location, "/")}/${local.resource_pool_name}")
}

# Creates base OS image for the machines
resource "libvirt_volume" "base" {
  name = "${var.resource_name_prefix}base-volume"
  pool = libvirt_pool.resource_pool.name

  source = pathexpand(var.machine_image_source)
}

locals {
  machine_user = "k0s"
}

module "controllers" {
  source = "./modules/machine"

  count = var.controller_num_nodes

  libvirt_provider_uri       = var.libvirt_provider_uri
  libvirt_resource_pool_name = libvirt_pool.resource_pool.name
  libvirt_base_volume_id     = libvirt_volume.base.id
  libvirt_network_id         = libvirt_network.network.id

  machine_name       = "${var.resource_name_prefix}controller-${count.index}"
  machine_dns_domain = libvirt_network.network.domain

  machine_num_cpus = var.controller_num_cpus
  machine_memory   = var.controller_memory

  machine_user           = local.machine_user
  machine_ssh_public_key = chomp(tls_private_key.ssh.public_key_openssh)
}

module "workers" {
  source = "./modules/machine"

  count = var.worker_num_nodes

  libvirt_provider_uri       = var.libvirt_provider_uri
  libvirt_resource_pool_name = libvirt_pool.resource_pool.name
  libvirt_base_volume_id     = libvirt_volume.base.id
  libvirt_network_id         = libvirt_network.network.id

  machine_name       = "${var.resource_name_prefix}worker-${count.index}"
  machine_dns_domain = libvirt_network.network.domain

  machine_num_cpus = var.worker_num_cpus
  machine_memory   = var.worker_memory

  machine_user           = local.machine_user
  machine_ssh_public_key = chomp(tls_private_key.ssh.public_key_openssh)
}

# module "loadbalancer" {
#   source = "./machine"

#   count = var.loadbalancer_enabled ? 1 : 0

#   libvirt_provider_uri       = var.libvirt_provider_uri
#   libvirt_resource_pool_name = libvirt_pool.resource_pool.name
#   libvirt_base_volume_id     = libvirt_volume.base.id
#   libvirt_network_id         = libvirt_network.network.id

#   machine_name       = "${var.resource_name_prefix}lb"
#   machine_dns_domain = libvirt_network.network.domain

#   machine_num_cpus = 1
#   machine_memory   = 128

#   machine_user           = var.machine_user
#   machine_ssh_public_key = chomp(tls_private_key.ssh.public_key_openssh)

#   cloudinit_extra_runcmds = [
#     "rc-update add haproxy boot",
#     "/etc/init.d/haproxy start",
#   ]

#   cloudinit_extra_user_data = {
#     write_files = [{
#       path = "/etc/haproxy/haproxy.cfg",
#       content = templatefile("${path.module}/haproxy.cfg.tftpl", {
#         k8s_api_port             = local.k8s_api_port,
#         k0s_api_port             = local.k0s_api_port,
#         konnectivity_server_port = local.konnectivity_server_port,

#         # This is a hack, since the file is only generated on first boot.
#         # Just add 5 controllers by default and let HAProxy resolve the IPs via DNS.
#         # controllers = module.controllers.*.info
#         controllers = [
#           { name = "${var.resource_name_prefix}controller-0" },
#           { name = "${var.resource_name_prefix}controller-1" },
#           { name = "${var.resource_name_prefix}controller-2" },
#           { name = "${var.resource_name_prefix}controller-3" },
#           { name = "${var.resource_name_prefix}controller-4" },
#         ],
#       }),
#     }]
#   }
# }
