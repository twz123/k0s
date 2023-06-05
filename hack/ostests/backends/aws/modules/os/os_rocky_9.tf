# https://rockylinux.org/cloud-images/

data "aws_ami" "rocky_9" {
  count = var.os == "rocky_9" ? 1 : 0

  owners      = ["792107900819"]
  name_regex  = "^Rocky-9-EC2-Base-9\\.2-\\d+\\.\\d+\\.x86_64"
  most_recent = true

  filter {
    name   = "name"
    values = ["Rocky-9-EC2-Base-9.2-*.x86_64"]
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
  os_rocky_9 = merge({
    id           = var.os
    ssh_username = "rocky"
    },
    var.os != "rocky_9" ? {} : {
      controller_ami = {
        id = data.aws_ami.rocky_9.0.id
      }
      worker_ami = {
        id = data.aws_ami.rocky_9.0.id
      }
    },
  )
}
