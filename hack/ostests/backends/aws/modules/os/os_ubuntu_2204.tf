# https://ubuntu.com/server/docs/cloud-images/amazon-ec2

data "aws_ami" "ubuntu_2204" {
  count = var.os == "ubuntu_2204" ? 1 : 0

  owners      = ["099720109477"]
  name_regex  = "ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-\\d+"
  most_recent = true

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
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
  os_ubuntu_2204 = var.os != "ubuntu_2204" ? {} : {
    ami_configs = {
      default = {
        id            = one(data.aws_ami.ubuntu_2204.*.id)
        instance_type = "t2.medium"

        connection = {
          type     = "ssh"
          username = "ubuntu"
        }
      }
    }
  }
}
