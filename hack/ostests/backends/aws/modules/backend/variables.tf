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
    ami_configs = object({
      default = object({
        id            = string
        instance_type = string

        user_data    = optional(string)
        ready_script = optional(string)

        connection = object({
          type     = string
          username = string
        })
      })
      controller        = optional(map(any))
      controller_worker = optional(map(any))
      worker            = optional(map(any))
    })
  })

  description = "The OS configuration."
}

# Controller node parameters

variable "controller_num_nodes" {
  type        = number
  description = "The number controller nodes to spin up."
  default     = 1
}

variable "controller_worker_num_nodes" {
  type        = number
  description = "The number controller+worker nodes to spin up."
  default     = 2
}

# Worker node parameters

variable "worker_num_nodes" {
  type        = number
  description = "The number worker nodes to spin up."
  default     = 1
}

# # Load balancer variables

# variable "loadbalancer_enabled" {
#   type        = bool
#   description = "Whether to provision a load balancer in front of the control plane."
#   default     = false
# }
