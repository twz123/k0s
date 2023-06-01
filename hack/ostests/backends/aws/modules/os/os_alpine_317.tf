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
    name   = "root-device-type"
    values = ["ebs"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

locals {
  os_alpine_317 = merge({
    id           = var.os
    ssh_username = "alpine"
    },
    var.os != "alpine_317" ? {} : {
      controller_ami = {
        id        = data.aws_ami.alpine_317.0.id
        user_data = templatefile("${path.module}/os_alpine_317_userdata.tftpl", { worker = false })
      }
      worker_ami = {
        id        = data.aws_ami.alpine_317.0.id
        user_data = templatefile("${path.module}/os_alpine_317_userdata.tftpl", { worker = true })
      }
    }
  )
}
