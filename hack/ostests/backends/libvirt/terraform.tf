terraform {
  required_version = ">= 1.4.0"

  required_providers {
    libvirt = { source = "dmacvicar/libvirt", version = "~> 0.7.0", }
    local   = { source = "hashicorp/local", version = "~> 2.0", }
  }
}
