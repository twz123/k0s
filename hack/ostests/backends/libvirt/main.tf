provider "libvirt" {
  uri = var.libvirt_provider_uri
}

resource "random_pet" "resource_name_prefix" {
  count = var.resource_name_prefix == null ? 1 : 0
}

locals {
  resource_name_prefix = (var.resource_name_prefix != null
    ? var.resource_name_prefix
    : format("%s-", random_pet.resource_name_prefix.0.id)
  )
  workdir = (var.workdir != null
    ? pathexpand(var.workdir)
    : pathexpand("~/.cache/k0s-ostests/libvirt/${local.resource_name_prefix}files")
  )
}

module "backend" {
  source = "./modules/backend"

  resource_name_prefix = local.resource_name_prefix
}

resource "local_sensitive_file" "ssh_private_key" {
  content         = module.backend.ssh_private_key
  filename        = "${local.workdir}/ssh-private-key.pem"
  file_permission = "0400"
}

module "k0sctl" {
  source = "../../modules/k0sctl"

  hosts                    = module.backend.machines
  ssh_username             = module.backend.ssh_username
  ssh_private_key_filename = local_sensitive_file.ssh_private_key.filename
}
