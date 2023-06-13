variable "os" {
  type        = string
  description = "The OS which is to be configured."
}

variable "cloudwatch_agent_config" {
  type        = any
  description = "The CloudWatch agent config to provision in the images, if any."
}
