output "machines" {
  value = local.machines
}

output "ssh_username" {
  value       = local.ssh_username
  description = "The username that can be used to authenticate via SSH."
}

output "ssh_private_key" {
  value       = tls_private_key.ssh.private_key_pem
  sensitive   = true
  description = "The private key that can be used to authenticate via SSH."
}

# output "loadbalancer" {
#   value = one(module.loadbalancer.*.info)
# }

