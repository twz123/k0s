# https://docs.fedoraproject.org/en-US/fedora-coreos/provisioning-aws/

data "aws_ami" "fcos_38" {
  count = var.os == "fcos_38" ? 1 : 0

  owners      = ["125523088429"]
  name_regex  = "^fedora-coreos-38\\.\\d+\\..+-x86_64"
  most_recent = true

  filter {
    name   = "name"
    values = ["fedora-coreos-38.*.*-x86_64"]
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
  os_fcos_38 = merge({
    id           = var.os
    ssh_username = "core"
    },
    var.os != "fcos_38" ? {} : {
      controller_ami = {
        id = data.aws_ami.fcos_38.0.id
      }
      worker_ami = {
        id = data.aws_ami.fcos_38.0.id
      }
    },
  )
}
