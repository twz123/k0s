resource "tls_private_key" "ssh" {
  algorithm = "ED25519"
}

resource "aws_key_pair" "ssh" {
  key_name   = "${var.resource_name_prefix}-ssh"
  public_key = tls_private_key.ssh.public_key_openssh
}

# Find the latest Canonical Ubuntu 22.04 image
data "aws_ami" "os" {
  name_regex  = "ubuntu-jammy"
  most_recent = true

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  # Owner = Canonical
  owners = ["099720109477"]
}

resource "aws_instance" "controllers" {
  count = var.controller_num_nodes

  ami           = data.aws_ami.os.id
  instance_type = var.controller_aws_instance_type
  subnet_id     = data.aws_subnet.az_default.id

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

  ami           = data.aws_ami.os.id
  instance_type = var.worker_aws_instance_type
  subnet_id     = data.aws_subnet.az_default.id

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
