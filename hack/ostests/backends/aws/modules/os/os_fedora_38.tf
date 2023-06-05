# https://alt.fedoraproject.org/cloud/

data "aws_ami" "fedora_38" {
  count = var.os == "fedora_38" ? 1 : 0

  owners      = ["125523088429"]
  name_regex  = "^Fedora-Cloud-Base-38-.+\\.x86_64-hvm-"
  most_recent = true

  filter {
    name   = "name"
    values = ["Fedora-Cloud-Base-38-*.x86_64-hvm-*"]
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
  os_fedora_38 = merge({
    id           = var.os
    ssh_username = "fedora"
    },
    var.os != "fedora_38" ? {} : {
      controller_ami = {
        id = data.aws_ami.fedora_38.0.id
      }
      worker_ami = {
        id = data.aws_ami.fedora_38.0.id
      }
    },
  )
}
