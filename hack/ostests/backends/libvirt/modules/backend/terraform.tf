terraform {
  required_version = ">= 1.4.0"

  required_providers {
    libvirt = { source = "dmacvicar/libvirt", version = "~> 0.7.0", }
    random  = { source = "hashicorp/random", version = "~> 3.0", }
    tls     = { source = "hashicorp/tls", version = "~> 4.0", }
  }
}
