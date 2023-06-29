variable "hosts" {
  type = list(
    object({
      name          = string,
      role          = string,
      is_controller = bool,
      is_worker     = bool,
      ipv4          = optional(string),
      ipv6          = optional(string),
      connection = object({
        type     = string
        username = string
      })
    })
  )
  nullable = false

  description = "The hosts to be provisoned by k0sctl."
}

variable "ssh_private_key_filename" {
  type        = string
  nullable    = false
  description = "The name of the private key file used to authenticate via SSH."

  validation {
    condition     = length(var.ssh_private_key_filename) != 0
    error_message = "SSH private key file name may not be empty."
  }
}

variable "k0sctl_executable_path" {
  type        = string
  nullable    = false
  description = "Path to the k0sctl executable to use for local-exec provisioning."
  default     = "k0sctl"

  validation {
    condition     = length(var.k0sctl_executable_path) != 0
    error_message = "Path to the k0sctl executable may not be empty."
  }
}

variable "k0s_version" {
  type        = string
  nullable    = false
  description = "The k0s version to deploy on the hosts. May be an exact version, \"stable\" or \"latest\"."

  validation {
    condition     = length(var.k0s_version) != 0
    error_message = "The k0s version may not be empty."
  }
}

variable "k0s_executable_path" {
  type        = string
  description = "Path to the k0s executable to use, or null if it should be managed by k0sctl."
  default     = null

  validation {
    condition     = var.k0s_executable_path == null ? true : length(var.k0s_executable_path) != 0
    error_message = "Path to the k0sctl executable may not be empty."
  }
}

variable "k0s_install_flags" {
  type        = list(string)
  nullable    = false
  description = "Install flags to be passed to k0s."
  default     = []
}

variable "k0s_controller_install_flags" {
  type        = list(string)
  nullable    = false
  description = "Install flags to be passed to k0s controllers."
  default     = []
}

variable "k0s_worker_install_flags" {
  type        = list(string)
  nullable    = false
  description = "Install flags to be passed to k0s workers."
  default     = []
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
      podCIDR  = optional(string),
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
  nullable    = false
  description = "The k0s config spec"
  default     = {}
}
