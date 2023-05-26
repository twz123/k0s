locals {
  network_ipv4_auto = (var.network_ipv4_cidr == null && var.network_ipv4_cidr == null)
  network_ipv4_cidr = (local.network_ipv4_auto
    ? format("10.%s.%s.0/24", random_integer.network_ipv4.0.id, random_integer.network_ipv4.1.id)
    : var.network_ipv4_cidr
  )
}

resource "random_integer" "network_ipv4" {
  count = local.network_ipv4_auto ? 2 : 0
  min   = 1
  max   = 254
}

resource "libvirt_network" "network" {
  name = "${var.resource_name_prefix}-network"

  mode      = "nat"
  autostart = true
  addresses = [for addr in [
    local.network_ipv4_cidr,
    var.network_ipv6_cidr,
    ] : addr if addr != null
  ]

  domain = var.network_dns_domain == null ? "${var.resource_name_prefix}-net.local" : var.network_dns_domain

  dns {
    enabled    = true
    local_only = false
  }

  dhcp {
    enabled = true
  }
}
