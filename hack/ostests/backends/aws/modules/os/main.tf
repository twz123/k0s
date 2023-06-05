locals {
  # Boilerplate to make terraform a little bit dynamic

  os = {
    alpine_317  = local.os_alpine_317
    centos_7    = local.os_centos_7
    centos_8    = local.os_centos_8
    centos_9    = local.os_centos_9
    debian_10   = local.os_debian_10
    debian_11   = local.os_debian_11
    rhel_7      = local.os_rhel_7
    rhel_8      = local.os_rhel_8
    rhel_9      = local.os_rhel_9
    rocky_8     = local.os_rocky_8
    rocky_9     = local.os_rocky_9
    ubuntu_2204 = local.os_ubuntu_2204
  }
}
