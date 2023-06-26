resource "tls_private_key" "ssh" {
  algorithm = "ED25519"
}

resource "aws_key_pair" "ssh" {
  key_name   = "${var.resource_name_prefix}-ssh"
  public_key = tls_private_key.ssh.public_key_openssh
}

locals {
  default_node_config = {
    instance_type = "t3a.small"
  }

  machine_roles = {
    controller = {
      count       = var.controller_num_nodes
      node_config = merge(local.default_node_config, var.os.node_configs.default, var.os.node_configs.controller)
    }

    "controller+worker" = {
      count       = var.controller_worker_num_nodes
      node_config = merge(local.default_node_config, var.os.node_configs.default, var.os.node_configs.worker, var.os.node_configs.controller_worker)
    }

    worker = {
      count       = var.worker_num_nodes
      node_config = merge(local.default_node_config, var.os.node_configs.default, var.os.node_configs.worker)
    }
  }

  machines = merge([for role, params in local.machine_roles : {
    for idx in range(params.count) : "${role}-${idx + 1}" => {
      role        = role
      node_config = params.node_config
    }
  }]...)
}

resource "aws_instance" "machines" {
  for_each = local.machines

  ami           = each.value.node_config.ami_id
  instance_type = each.value.node_config.instance_type
  subnet_id     = data.aws_subnet.default_for_selected_az.id

  user_data = each.value.node_config.user_data

  tags = {
    Name                              = "${var.resource_name_prefix}-${each.key}"
    "ostests.k0sproject.io/node-name" = each.key
    "k0sctl.k0sproject.io/host-role"  = each.value.role
  }

  key_name                    = aws_key_pair.ssh.key_name
  vpc_security_group_ids      = [aws_security_group.all_access.id]
  associate_public_ip_address = true
  source_dest_check           = !contains(["controller+worker", "worker"], each.value.role)

  root_block_device {
    volume_type = "gp2"
    volume_size = 20
  }
}

resource "terraform_data" "ready_scripts" {
  for_each = { for name, params in local.machines : name => params if params.node_config.ready_script != null }

  connection {
    type        = each.value.node_config.connection.type
    user        = each.value.node_config.connection.username
    private_key = tls_private_key.ssh.private_key_pem
    host        = aws_instance.machines[each.key].public_ip
    agent       = false
  }

  provisioner "remote-exec" {
    inline = [each.value.node_config.ready_script]
  }
}

resource "terraform_data" "provisioned_machines" {
  depends_on = [terraform_data.ready_scripts]

  input = [for machine in aws_instance.machines : {
    name       = machine.tags.Name,
    ipv4       = machine.public_ip,
    role       = machine.tags["k0sctl.k0sproject.io/host-role"]
    connection = local.machines[machine.tags["ostests.k0sproject.io/node-name"]].node_config.connection
  }]
}
