# https://centos.org/download/aws-images/

data "aws_ami" "centos_9" {
  count = var.os == "centos_9" ? 1 : 0

  owners      = ["125523088429"]
  name_regex  = "^CentOS Stream 9 x86_64 \\d+"
  most_recent = true

  filter {
    name   = "name"
    values = ["CentOS Stream 9 x86_64 *"]
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
  os_centos_9 = merge({
    id           = var.os
    ssh_username = "ec2-user"
    },
    var.os != "centos_9" ? {} : {
      controller_ami = {
        id = data.aws_ami.centos_9.0.id
      }
      worker_ami = {
        id = data.aws_ami.centos_9.0.id
      }
    },
  )
}
