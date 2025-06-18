variable "os" {
  type        = string
  description = "The OS which is to be configured."
}

variable "arch" {
  type        = string
  description = "The architecture for the OS."
}

variable "default_instance_size" {
  type        = string
  description = "The default instance size to choose."

  validation {
    condition     = contains(["small", "medium", "large", "xlarge", "2xlarge"], var.default_instance_size)
    error_message = "Allowed values for default_instance_size are: small, medium, large, xlarge and 2xlarge."
  }
}

variable "additional_ingress_cidrs" {
  type        = list(string)
  nullable    = false
  description = "Additional CIDRs that are allowed for ingress traffic."
  default     = []
}
