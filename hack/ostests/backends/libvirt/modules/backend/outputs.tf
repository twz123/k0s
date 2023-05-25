output "machines" {
  value = local.machines
}

output "ssh_username" {
  value = local.ssh_username
}

output "ssh_private_key" {
  value     = tls_private_key.ssh.private_key_pem
  sensitive = true
}

# output "loadbalancer" {
#   value = one(module.loadbalancer.*.info)
# }

