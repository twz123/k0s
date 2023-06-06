# https://www.alpinelinux.org/cloud/

data "aws_ami" "alpine_317" {
  count = var.os == "alpine_317" ? 1 : 0

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
  os_alpine_317 = var.os != "alpine_317" ? {} : {
    node_configs = {
      default = {
        ami_id        = one(data.aws_ami.alpine_317.*.id)
        instance_type = "t2.medium"

        user_data    = templatefile("${path.module}/os_alpine_317_userdata.tftpl", { worker = true })
        ready_script = file("${path.module}/os_alpine_317_ready.sh")

        connection = {
          type     = "ssh"
          username = "alpine"
        }
      }
      controller = {
        user_data = templatefile("${path.module}/os_alpine_317_userdata.tftpl", { worker = false })
      }
    }
  }
}
