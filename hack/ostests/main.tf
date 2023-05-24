
# module "k0sctl" {
#   source = "./modules/k0sctl"
# }

locals {
  resource_name_prefix = (var.resource_name_prefix != null
    ? var.resource_name_prefix
    : format("%s-", random_pet.resource_name_prefix.0.id)
  )
}

resource "random_pet" "resource_name_prefix" {
  count = var.resource_name_prefix == null ? 1 : 0
}

module "libvirt" {
  source = "./modules/libvirt"

  resource_name_prefix = local.resource_name_prefix
}
