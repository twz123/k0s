output "machines" {
  value       = terraform_data.provisioned_machines.output
  description = "The machines that have been provisioned."
}

output "ssh_username" {
  value       = var.os.ssh_username
  description = "The username that can be used to authenticate via SSH."
}

output "ssh_private_key" {
  value       = tls_private_key.ssh.private_key_openssh
  sensitive   = true
  description = "The private key that can be used to authenticate via SSH."
}
