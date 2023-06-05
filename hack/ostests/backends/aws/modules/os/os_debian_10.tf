# https://wiki.debian.org/Cloud/AmazonEC2Image/Buster

data "aws_ami" "debian_10" {
  count = var.os == "debian_10" ? 1 : 0

  owners      = ["136693071363"]
  name_regex  = "^debian-10-amd64-\\d+-\\d+$"
  most_recent = true

  filter {
    name   = "name"
    values = ["debian-10-amd64-*-*"]
  }

  filter {
    name   = "architecture"
    values = ["x86_64"]
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
  os_debian_10 = merge({
    id           = var.os
    ssh_username = "admin"
    },
    var.os != "debian_10" ? {} : {
      controller_ami = {
        id = data.aws_ami.debian_10.0.id
      }
      worker_ami = {
        id = data.aws_ami.debian_10.0.id
      }
    },
  )
}
