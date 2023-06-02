# https://centos.org/download/aws-images/

data "aws_ami" "centos_8" {
  count = var.os == "centos_8" ? 1 : 0

  owners      = ["125523088429"]
  name_regex  = "^CentOS Stream 8 x86_64 \\d+"
  most_recent = true

  filter {
    name   = "name"
    values = ["CentOS Stream 8 x86_64 *"]
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
  os_centos_8 = merge({
    id           = var.os
    ssh_username = "centos"
    },
    var.os != "centos_8" ? {} : {
      controller_ami = {
        id = data.aws_ami.centos_8.0.id
      }
      worker_ami = {
        id = data.aws_ami.centos_8.0.id
      }
    },
  )
}
