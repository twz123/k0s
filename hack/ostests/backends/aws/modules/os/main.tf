locals {
  # Boilerplate to make terraform a little bit dynamic

  os = {
    alpine_317  = local.os_alpine_317
    ubuntu_2204 = local.os_ubuntu_2204
  }
}
