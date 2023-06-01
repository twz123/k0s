output "machines" {
  value = [for machine in concat(aws_instance.controllers.*, aws_instance.workers.*) : {
    name = machine.tags.Name,
    ipv4 = machine.public_ip,
    role = machine.tags["k0sctl.k0sproject.io/host-role"]
  }]
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
