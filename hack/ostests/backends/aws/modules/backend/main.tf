resource "tls_private_key" "ssh" {
  algorithm = "ED25519"
}

resource "aws_key_pair" "ssh" {
  key_name   = "${var.resource_name_prefix}-ssh"
  public_key = tls_private_key.ssh.public_key_openssh
}

resource "terraform_data" "controllers" {
  count = var.controller_num_nodes

  input = {
    name = format("%s-controller-%d", var.resource_name_prefix, count.index)
    role = "controller"
    ami  = merge(var.os.ami_configs.default, var.os.ami_configs.controller)
  }
}

resource "terraform_data" "controller_workers" {
  count = var.controller_worker_num_nodes

  input = {
    name = format("%s-controller-%d", var.resource_name_prefix, var.controller_num_nodes + count.index)
    role = "controller+worker"
    ami  = merge(var.os.ami_configs.default, var.os.ami_configs.worker, var.os.ami_configs.controller_worker)
  }
}

resource "terraform_data" "workers" {
  count = var.worker_num_nodes

  input = {
    name = format("%s-worker-%d", var.resource_name_prefix, count.index)
    role = "worker"
    ami  = merge(var.os.ami_configs.default, var.os.ami_configs.worker)
  }
}

locals {
  machines = { for idx, machine in concat(
    terraform_data.controllers.*.output,
    terraform_data.controller_workers.*.output,
    terraform_data.workers.*.output
  ) : "machine-${idx}" => machine }
}

resource "aws_instance" "machines" {
  for_each = local.machines

  ami           = each.value.ami.id
  instance_type = each.value.ami.instance_type
  subnet_id     = data.aws_subnet.az_default.id

  user_data = each.value.ami.user_data

  tags = {
    Name                             = each.value.name
    "k0sctl.k0sproject.io/host-role" = each.value.role
  }

  key_name                    = aws_key_pair.ssh.key_name
  vpc_security_group_ids      = [aws_security_group.all_access.id]
  associate_public_ip_address = true

  root_block_device {
    volume_type = "gp2"
    volume_size = 20
  }
}

resource "terraform_data" "ready_scripts" {
  for_each = local.machines

  connection {
    type        = each.value.ami.connection.type
    user        = each.value.ami.connection.username
    private_key = tls_private_key.ssh.private_key_pem
    host        = one([for i in aws_instance.machines : i if i.tags.Name == each.value.name]).public_ip
    agent       = false
  }

  provisioner "remote-exec" {
    inline = each.value.ami.ready_script != null ? [each.value.ami.ready_script] : []
  }
}

resource "terraform_data" "provisioned_machines" {
  depends_on = [terraform_data.ready_scripts]

  input = [for machine in aws_instance.machines : {
    name       = machine.tags.Name,
    ipv4       = machine.public_ip,
    role       = machine.tags["k0sctl.k0sproject.io/host-role"]
    connection = one([for m in local.machines : m if m.name == machine.tags.Name]).ami.connection
  }]
}
