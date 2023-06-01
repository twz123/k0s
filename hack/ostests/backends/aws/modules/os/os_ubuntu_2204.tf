data "aws_ami" "ubuntu_2204" {
  count = var.os == "ubuntu_2204" ? 1 : 0

  owners      = ["099720109477"]
  name_regex  = "ubuntu-jammy"
  most_recent = true

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
  }

  filter {
    name   = "root-device-type"
    values = ["ebs"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

locals {
  os_ubuntu_2204 = merge({
    id           = var.os
    ssh_username = "ubuntu"
    },
    var.os != "ubuntu_2204" ? {} : {
      controller_ami = {
        id = data.aws_ami.ubuntu_2204.0.id
      }
      worker_ami = {
        id = data.aws_ami.ubuntu_2204.0.id
      }
    },
  )
}
