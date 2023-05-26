provider "aws" {
  # Configure externally, no need to declare everything here:
  # https://registry.terraform.io/providers/hashicorp/aws/4.67.0/docs#authentication-and-configuration

  default_tags {
    tags = {
      "ostests.k0sproject.io/instance" = local.resource_name_prefix
    }
  }
}

resource "random_pet" "resource_name_prefix" {
  count = var.resource_name_prefix == null ? 1 : 0
}

locals {
  resource_name_prefix = (var.resource_name_prefix != null
    ? var.resource_name_prefix
    : random_pet.resource_name_prefix.0.id
  )

  cache_dir = (var.cache_dir != null
    ? pathexpand(var.cache_dir)
    : pathexpand("~/.cache/k0s-ostests")
  )
}

module "backend" {
  source = "./modules/backend"

  resource_name_prefix = local.resource_name_prefix
}

resource "local_sensitive_file" "ssh_private_key" {
  content         = module.backend.ssh_private_key
  filename        = "${local.cache_dir}/aws-${local.resource_name_prefix}-ssh-private-key.pem"
  file_permission = "0400"
}

module "k0sctl" {
  source = "../../modules/k0sctl"

  hosts                    = module.backend.machines
  ssh_username             = module.backend.ssh_username
  ssh_private_key_filename = local_sensitive_file.ssh_private_key.filename
}
