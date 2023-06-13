# https://blogs.oracle.com/linux/post/running-oracle-linux-in-public-clouds
# https://forums.oracle.com/ords/apexds/post/launch-an-oracle-linux-instance-in-aws-9462

data "aws_ami" "oracle_9_1" {
  count = var.os == "oracle_9_1" ? 1 : 0

  owners      = ["131827586825"]
  name_regex  = "^OL9\\.1-x86_64-HVM-"
  most_recent = true

  filter {
    name   = "name"
    values = ["OL9.1-x86_64-HVM-*"]
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
  os_oracle_9_1 = var.os != "oracle_9_1" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.oracle_9_1.*.id)

        connection = {
          type     = "ssh"
          username = "ec2-user"
        }
      }
    }
  }
}
