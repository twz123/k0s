terraform {
  required_version = "~> 1.3.0"

  required_providers {
    libvirt = { source = "dmacvicar/libvirt", version = "~> 0.7.0", }
    random  = { source = "hashicorp/random", version = "~> 3.0", }
  }
}
