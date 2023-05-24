locals {
  resource_name_prefix = (var.resource_name_prefix != null
    ? var.resource_name_prefix
    : format("%s-", random_pet.resource_name_prefix.0.id)
  )
  workdir = (var.workdir != null
    ? pathexpand(var.workdir)
    : pathexpand("~/.cache/k0s-ostests/${local.resource_name_prefix}files")
  )
}

resource "random_pet" "resource_name_prefix" {
  count = var.resource_name_prefix == null ? 1 : 0
}

module "libvirt" {
  source = "./modules/libvirt"

  resource_name_prefix = local.resource_name_prefix
}

module "k0sctl" {
  source = "./modules/k0sctl"

  workdir         = local.workdir
  hosts           = module.libvirt.machines
  ssh_username    = module.libvirt.ssh_username
  ssh_private_key = module.libvirt.ssh_private_key
}
