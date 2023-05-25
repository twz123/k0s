output "hosts" {
  value       = var.hosts
  description = "The hosts that have been provisioned by k0sctl."
}

output "ssh_username" {
  value       = var.ssh_username
  description = "The username that has been used to authenticate via SSH."
}

output "ssh_private_key_filename" {
  value       = var.ssh_private_key_filename
  description = "The name of the private key file that has been used to authenticate via SSH."
}
