resource "tls_private_key" "ssh" {
  algorithm = "ED25519"
}

resource "aws_key_pair" "ssh" {
  key_name   = "${var.resource_name_prefix}-ssh"
  public_key = tls_private_key.ssh.public_key_openssh
}

resource "aws_instance" "controllers" {
  count = var.controller_num_nodes

  ami           = var.os.controller_ami.id
  instance_type = var.controller_aws_instance_type
  subnet_id     = data.aws_subnet.az_default.id

  user_data = var.os.controller_ami.user_data

  tags = {
    Name                             = format("%s-controller-%d", var.resource_name_prefix, count.index)
    "k0sctl.k0sproject.io/host-role" = var.controller_k0s_enable_worker ? "controller+worker" : "controller"
  }

  key_name                    = aws_key_pair.ssh.key_name
  vpc_security_group_ids      = [aws_security_group.all_access.id]
  associate_public_ip_address = true

  root_block_device {
    volume_type = "gp2"
    volume_size = 20
  }
}

resource "aws_instance" "workers" {
  count = var.worker_num_nodes

  ami           = var.os.worker_ami.id
  instance_type = var.worker_aws_instance_type
  subnet_id     = data.aws_subnet.az_default.id

  user_data = var.os.worker_ami.user_data

  tags = {
    Name                             = format("%s-worker-%d", var.resource_name_prefix, count.index)
    "k0sctl.k0sproject.io/host-role" = "worker"
  }

  key_name                    = aws_key_pair.ssh.key_name
  vpc_security_group_ids      = [aws_security_group.all_access.id]
  associate_public_ip_address = true
  source_dest_check           = false

  root_block_device {
    volume_type = "gp2"
    volume_size = 20
  }
}

resource "terraform_data" "ready_scripts" {
  for_each = merge(
    var.os.controller_ami.ready_script == null ? {} : {
      for idx, c in aws_instance.controllers : "controller-${idx}" => {
        host = c.public_ip, script = var.os.controller_ami.ready_script
      }
    },
    var.os.worker_ami.ready_script == null ? {} : {
      for idx, w in aws_instance.workers : "worker-${idx}" => {
        host = w.public_ip, script = var.os.worker_ami.ready_script
      }
    },
  )

  connection {
    type        = "ssh"
    user        = var.os.ssh_username
    private_key = tls_private_key.ssh.private_key_pem
    host        = each.value.host
    agent       = false
  }

  provisioner "remote-exec" {
    inline = [each.value.script]
  }
}

resource "terraform_data" "provisioned_machines" {
  depends_on = [terraform_data.ready_scripts]

  input = [for machine in concat(aws_instance.controllers.*, aws_instance.workers.*) : {
    name = machine.tags.Name,
    ipv4 = machine.public_ip,
    role = machine.tags["k0sctl.k0sproject.io/host-role"]
  }]
}
