# https://access.redhat.com/solutions/15356

data "aws_ami" "rhel_8" {
  count = var.os == "rhel_8" ? 1 : 0

  owners      = ["309956199498"]
  name_regex  = "^RHEL-8\\.6\\.0_HVM-\\d+-x86_64-"
  most_recent = true

  filter {
    name   = "name"
    values = ["RHEL-8.6.0_HVM-*-x86_64-*"]
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
  os_rhel_8 = var.os != "rhel_8" ? {} : {
    ami_configs = {
      default = {
        id            = one(data.aws_ami.rhel_8.*.id)
        instance_type = "t2.medium"

        connection = {
          type     = "ssh"
          username = "ec2-user"
        }
      }
    }
  }
}
