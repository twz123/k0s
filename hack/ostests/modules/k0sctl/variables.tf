variable "hosts" {
  type = list(
    object({
      name = string,
      role = string,
      ipv4 = optional(string),
      ipv6 = optional(string),
      # hooks = optional(object({
      #   apply = optional(object({
      #     before = optional(list(string))
      #   }))
      # })),
    })
  )

  description = "The hosts to be provisoned by k0sctl."
}

variable "ssh_username" {
  type        = string
  description = "The username used to authenticate via SSH."

  validation {
    condition     = length(var.ssh_username) != 0
    error_message = "SSH username may not be empty."
  }
}

variable "ssh_private_key_filename" {
  type        = string
  description = "The name of the private key file used to authenticate via SSH."

  validation {
    condition     = length(var.ssh_private_key_filename) != 0
    error_message = "SSH private key file name may not be empty."
  }
}

# k0s variables
variable "k0s_version" {
  type        = string
  description = "The k0s version to deploy on the machines. May be an exact version, \"stable\" or \"latest\"."
  default     = "v1.27.2+k0s.0"
}

variable "k0s_dynamic_config" {
  type        = bool
  description = "Whether to enable k0s dynamic configuration."
  default     = false
}

variable "k0s_config_spec" {
  type = object({
    api = optional(object({
      extraArgs = map(string),
    })),
    extensions = optional(object({
      helm = optional(object({
        repositories = optional(list(
          object({
            name     = string,
            url      = string,
            caFile   = optional(string),
            certFile = optional(string),
            insecure = optional(bool),
            keyfile  = optional(string),
            username = optional(string),
            password = optional(string),
          }),
        )),
        charts = optional(list(
          object({
            name      = string,
            chartname = string,
            version   = optional(string),
            values    = optional(string),
            namespace = string,
            timeout   = optional(string),
          }),
        )),
      })),
    })),
    network = optional(object({
      provider = optional(string),
      calico   = optional(map(string)),
      nodeLocalLoadBalancing = optional(object({
        enabled = optional(bool),
        type    = optional(string),
        envoyProxy = optional(object({
          image = optional(object({
            image   = string,
            version = string,
          })),
          port = optional(number),
        })),
      })),
    })),
    images = optional(map(map(map(string)))),
  })
  description = "The k0s config spec"
  default     = {}

  validation {
    condition     = var.k0s_config_spec != null
    error_message = "K0s config spec may not be null."
  }
}

# k0sctl variables

variable "k0sctl_binary" {
  type        = string
  description = "Path to the k0sctl binary to use for local-exec provisioning."
  default     = "k0sctl"
  nullable    = false

  validation {
    condition     = length(var.k0sctl_binary) != 0
    error_message = "Path to the k0sctl binary may not be empty."
  }
}

variable "k0s_binary" {
  type        = string
  description = "Path to the k0s binary to use, or null if it should be downloaded."
  default     = null
}

# variable "airgap_image_bundle" {
#   type        = string
#   description = <<-EOD
#     Path to the airgap image bundle to be copied to the worker-enabled nodes, or null
#     if it should be downloaded. See https://docs.k0sproject.io/head/airgap-install/
#     for details on that.
#   EOD
#   default     = null
# }

variable "k0s_install_flags" {
  type        = list(string)
  description = "Install flags to be passed to k0s."
  default     = []

  validation {
    condition     = var.k0s_install_flags != null
    error_message = "K0s install flags may not be null."
  }
}

variable "k0s_controller_install_flags" {
  type        = list(string)
  description = "Install flags to be passed to k0s controllers."
  default     = []

  validation {
    condition     = var.k0s_controller_install_flags != null
    error_message = "K0s controller install flags may not be null."
  }
}

variable "k0s_worker_install_flags" {
  type        = list(string)
  description = "Install flags to be passed to k0s workers."
  default     = []

  validation {
    condition     = var.k0s_worker_install_flags != null
    error_message = "K0s worker install flags may not be null."
  }
}
