output "machines" {
  value = [for machine in concat(aws_instance.controllers.*, aws_instance.workers.*) : {
    name = machine.tags.Name,
    ipv4 = machine.public_ip,
  }]
}

output "ssh_private_key" {
  value     = tls_private_key.ssh.private_key_openssh
  sensitive = true
}
