output "os" {
  value       = local.os[var.os]
  description = "The OS confguration."
}

output "cloudwatch_agent_config" {
  value       = var.cloudwatch_agent_config
  description = "The CloudWatch agent config that has been provisioned to provision in the images, if any."
}
