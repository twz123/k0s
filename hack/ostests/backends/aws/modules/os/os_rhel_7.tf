# https://access.redhat.com/solutions/15356

data "aws_ami" "rhel_7" {
  count = var.os == "rhel_7" ? 1 : 0

  owners      = ["309956199498"]
  name_regex  = "^RHEL-7\\.9_HVM-\\d+-x86_64-"
  most_recent = true

  filter {
    name   = "name"
    values = ["RHEL-7.9_HVM-*-x86_64-*"]
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
  os_rhel_7 = merge({
    id           = var.os
    ssh_username = "ec2-user"
    },
    var.os != "rhel_7" ? {} : {
      controller_ami = {
        id = data.aws_ami.rhel_7.0.id
      }
      worker_ami = {
        id = data.aws_ami.rhel_7.0.id
      }
    },
  )
}
