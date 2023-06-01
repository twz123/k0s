variable "resource_name_prefix" {
  type        = string
  description = "Prefix to be prepended to all resource names."

  validation {
    condition     = var.resource_name_prefix != null && can(regex("^([a-z][a-z0-9-_]*)?$", var.resource_name_prefix))
    error_message = "Invalid resource prefix."
  }
}

variable "os" {
  type = object({
    ami = object({
      id        = string
      user_data = optional(string)
    })
    ssh_username = string
  })

  description = "The OS configuration."
}

# Controller node parameters

variable "controller_num_nodes" {
  type        = number
  description = "The number controller nodes to spin up."
  default     = 1
}

variable "controller_aws_instance_type" {
  type        = string
  description = "The AWS instance type to use for controller nodes."
  default     = "t2.medium"
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

variable "worker_aws_instance_type" {
  type        = string
  description = "The AWS instance type to use for worker nodes."
  default     = "t2.medium"
}

# # Load balancer variables

# variable "loadbalancer_enabled" {
#   type        = bool
#   description = "Whether to provision a load balancer in front of the control plane."
#   default     = false
# }
