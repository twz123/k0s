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
  os_fcos_38 = var.os != "fcos_38" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.fcos_38.*.id)

        user_data = var.cloudwatch_agent_config == null ? null : jsonencode({
          ignition = { version = "3.1.0" },

          storage = { files = [
            {
              path     = "/etc/k0s-ostests/cloudwatch-agent.json",
              mode     = 0644,
              contents = { source = "data:application/json;charset=UTF-8,${urlencode(jsonencode(var.cloudwatch_agent_config))}" }
            },
            {
              # https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html
              path = "/etc/containers/systemd/amazon-cloudwatch-agent.container",
              mode = 0644,
              contents = { source = "data:text/plain;charset=UTF-8,${urlencode(
                <<-EOF
                  [Unit]
                  Description=Amazon CloudWatch Agent enables you to collect and export host-level metrics and logs.

                  Wants=network-online.target
                  After=network-online.target

                  [Container]
                  Image=public.ecr.aws/cloudwatch-agent/cloudwatch-agent:latest
                  Network=host
                  Volume=/:/rootfs:ro
                  Volume=/sys:/sys:ro
                  Volume=/dev/disk:/dev/disk:ro
                  Volume=/etc/k0s-ostests/cloudwatch-agent.json:/etc/cwagentconfig/cwagentconfig.json:ro

                  [Service]
                  Restart=always
                  RestartSec=15s

                  [Install]
                  WantedBy=multi-user.target default.target
                  EOF
              )}" }
            },
          ] }
        })

        connection = {
          type     = "ssh"
          username = "core"
        }
      }
    }
  }
}
