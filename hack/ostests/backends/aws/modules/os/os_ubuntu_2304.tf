# https://ubuntu.com/server/docs/cloud-images/amazon-ec2

data "aws_ami" "ubuntu_2304" {
  count = var.os == "ubuntu_2304" ? 1 : 0

  owners      = ["099720109477"]
  name_regex  = "ubuntu/images/hvm-ssd/ubuntu-lunar-23.04-amd64-server-\\d+"
  most_recent = true

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-lunar-23.04-amd64-server-*"]
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
  os_ubuntu_2304 = merge({
    id           = var.os
    ssh_username = "ubuntu"
    },
    var.os != "ubuntu_2304" ? {} : {
      controller_ami = {
        id = data.aws_ami.ubuntu_2304.0.id
      }
      worker_ami = {
        id = data.aws_ami.ubuntu_2304.0.id
      }
    },
  )
}
