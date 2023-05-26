variable "resource_name_prefix" {
  type        = string
  description = "Prefix to be prepended to all resource names."

  validation {
    condition     = var.resource_name_prefix != null && can(regex("^([a-z][a-z0-9-_]*)?$", var.resource_name_prefix))
    error_message = "Invalid resource prefix."
  }
}

# General libvirt configuration



variable "resource_pool_location" {
  type        = string
  description = "Location where resource pool will be initialized"
  default     = "/var/lib/libvirt/pools/"

  validation {
    condition     = length(var.resource_pool_location) != 0
    error_message = "Libvirt resource pool location may not be empty."
  }
}

variable "network_ipv4_cidr" {
  type        = string
  description = "IPv4 CIDR of the libvirt network of the virtual machines."
  default     = null

  validation {
    condition     = var.network_ipv4_cidr == null || can(regex("^([0-9]|[1-9][0-9]|1[0-9][0-9]|2[0-4][0-9]|25[0-5])(.([0-9]|[1-9][0-9]|1[0-9][0-9]|2[0-4][0-9]|25[0-5])){3}/([1-9]|[1-2][0-9]|3[0-2])$", var.network_ipv4_cidr))
    error_message = "Invalid IPv4 network CIDR."
  }
}

variable "network_ipv6_cidr" {
  type        = string
  description = "IPv6 CIDR of the libvirt network of the virtual machines, or null to not specify any."
  default     = null

  validation {
    condition     = var.network_ipv6_cidr == null || can(regex("^[0-9a-f]{1,4}(:[0-9a-f]{1,4})+::/[0-9]+$", var.network_ipv6_cidr))
    error_message = "Invalid IPv6 network CIDR."
  }
}

variable "network_dns_domain" {
  type        = string
  description = "DNS domain of the libvirt network of the virtual machines, or null if a domain name should be auto-generated."
  default     = null

  validation {
    condition     = var.network_dns_domain == null || can(regex("^(([a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9\\-]*[a-zA-Z0-9])\\.)*([A-Za-z0-9]|[A-Za-z0-9][A-Za-z0-9\\-]*[A-Za-z0-9])$", var.network_dns_domain))
    error_message = "Invalid libvirt network DNS domain."
  }
}

# General virtual machine configuration

variable "machine_image_source" {
  type        = string
  description = "Image source, which can be path on host's filesystem or URL."
  #default     = "alpine-image/image.qcow2"
  default = "~/Repos/k0s-libvirt-machines/alpine-image/image.qcow2"

  validation {
    condition     = length(var.machine_image_source) != 0
    error_message = "Virtual machine image source is missing."
  }
}

# variable "ssh_user" {
#   type        = string
#   description = "Username used to SSH into virtual machines."
#   default     = "k0s"
# }

# Controller node parameters

variable "controller_num_nodes" {
  type        = number
  description = "The number controller nodes to spin up."
  default     = 1
}

variable "controller_num_cpus" {
  type        = number
  description = "The number CPUs allocated to a controller node."
  default     = 1
}

variable "controller_memory" {
  type        = number
  description = "The amount of RAM (in MiB) allocated to a controller node."
  default     = 1024
}

variable "controller_k0s_enable_worker" {
  type        = bool
  description = "Whether k0s on the controllers should also schedule workloads."
  default     = false
}

# Worker node parameters

variable "worker_num_nodes" {
  type        = number
  description = "The number worker nodes to spin up."
  default     = 1
}

variable "worker_num_cpus" {
  type        = number
  description = "The number CPUs allocated to a worker node."
  default     = 1
}

variable "worker_memory" {
  type        = number
  description = "The amount of RAM (in MiB) allocated to a worker node."
  default     = 1024
}

# Load balancer variables

# variable "loadbalancer_enabled" {
#   type        = bool
#   description = "Whether to provision a load balancer in front of the control plane."
#   default     = false
# }
