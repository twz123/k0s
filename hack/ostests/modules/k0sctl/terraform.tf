terraform {
  required_version = ">= 1.4.0"

  required_providers {
    external = { source = "hashicorp/external", version = "~> 2.0", }
    local    = { source = "hashicorp/local", version = "~> 2.0", }
  }
}
