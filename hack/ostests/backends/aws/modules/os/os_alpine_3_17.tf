# https://www.alpinelinux.org/cloud/

data "aws_ami" "alpine_3_17" {
  count = var.os == "alpine_3_17" ? 1 : 0

  owners      = ["538276064493"]
  name_regex  = "^alpine-3\\.17\\.\\d+-x86_64-bios-tiny($|-.*)"
  most_recent = true

  filter {
    name   = "name"
    values = ["alpine-3.17.*-x86_64-bios-tiny*"]
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
  os_alpine_3_17 = var.os != "alpine_3_17" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.alpine_3_17.*.id)

        user_data = templatefile("${path.module}/os_alpine_3_17_userdata.tftpl", {
          worker                  = true
          cloudwatch_agent_config = var.cloudwatch_agent_config
        })
        ready_script = file("${path.module}/os_alpine_3_17_ready.sh")

        connection = {
          type     = "ssh"
          username = "alpine"
        }
      }
      controller = {
        user_data = templatefile("${path.module}/os_alpine_3_17_userdata.tftpl", {
          worker                  = false
          cloudwatch_agent_config = var.cloudwatch_agent_config
        })
      }
    }
  }
}
