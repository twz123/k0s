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
  name = "${var.resource_name_prefix}-resource-pool"

  type = "dir"
  path = pathexpand("${trimsuffix(var.resource_pool_location, "/")}/${var.resource_name_prefix}-resource-pool")
}

# Creates base OS image for the machines
resource "libvirt_volume" "base" {
  name = "${var.resource_name_prefix}-base-volume"
  pool = libvirt_pool.resource_pool.name

  source = pathexpand(var.machine_image_source)
}

locals {
  ssh_username = "k0s"

  machines = concat(
    [for machine in module.controllers.*.info :
      merge(machine, {
        role = var.controller_k0s_enable_worker ? "controller+worker" : "controller"
      })
    ],
    [for machine in module.workers.*.info :
      merge(machine, {
        role = "worker"
      })
    ],
  )
}

module "controllers" {
  source = "./modules/machine"

  count = var.controller_num_nodes

  libvirt_resource_pool_name = libvirt_pool.resource_pool.name
  libvirt_base_volume_id     = libvirt_volume.base.id
  libvirt_network_id         = libvirt_network.network.id

  machine_name       = "${var.resource_name_prefix}-controller-${count.index}"
  machine_dns_domain = libvirt_network.network.domain

  machine_num_cpus = var.controller_num_cpus
  machine_memory   = var.controller_memory

  ssh_username   = local.ssh_username
  ssh_public_key = chomp(tls_private_key.ssh.public_key_openssh)
}

module "workers" {
  source = "./modules/machine"

  count = var.worker_num_nodes

  libvirt_resource_pool_name = libvirt_pool.resource_pool.name
  libvirt_base_volume_id     = libvirt_volume.base.id
  libvirt_network_id         = libvirt_network.network.id

  machine_name       = "${var.resource_name_prefix}-worker-${count.index}"
  machine_dns_domain = libvirt_network.network.domain

  machine_num_cpus = var.worker_num_cpus
  machine_memory   = var.worker_memory

  ssh_username   = local.ssh_username
  ssh_public_key = chomp(tls_private_key.ssh.public_key_openssh)
}
