# https://www.flatcar.org/docs/latest/installing/cloud/aws-ec2/

data "aws_ami" "flatcar" {
  count = var.os == "flatcar" ? 1 : 0

  owners      = ["075585003325"]
  name_regex  = "^Flatcar-stable-\\d+\\..+-hvm"
  most_recent = true

  filter {
    name   = "name"
    values = ["Flatcar-stable-*.*-hvm"]
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
  os_flatcar = var.os != "flatcar" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.flatcar.*.id)

        user_data = var.cloudwatch_agent_config == null ? null : jsonencode({
          ignition = { version = "2.2.0" },

          storage = {
            files = [{
              filesystem = "root",
              path       = "/etc/k0s-ostests/cloudwatch-agent.json",
              mode       = 0644,
              contents   = { source = "data:application/json;charset=UTF-8,${urlencode(jsonencode(var.cloudwatch_agent_config))}" }
            }]
          }

          systemd = {
            units = [{
              name     = "amazon-cloudwatch-agent.service",
              enabled  = true,
              contents = <<-EOF
                [Unit]
                Description=Amazon CloudWatch Agent enables you to collect and export host-level metrics and logs.

                Wants=docker.socket
                After=docker.service

                Wants=network-online.target
                After=network-online.target

                [Service]
                ExecStartPre=/usr/bin/docker pull public.ecr.aws/cloudwatch-agent/cloudwatch-agent:latest
                ExecStart=/bin/sh -xc 'exec /usr/bin/docker run --rm --name="$1.$INVOCATION_ID" --net host -v /:/rootfs:ro -v /sys:/sys:ro -v /dev/disk:/dev/disk:ro -v /etc/k0s-ostests/cloudwatch-agent.json:/etc/cwagentconfig/cwagentconfig.json:ro public.ecr.aws/cloudwatch-agent/cloudwatch-agent:latest' -- %p
                ExecStop=/bin/sh -xc '/usr/bin/docker stop "$1.$INVOCATION_ID"' -- %p
                Restart=always
                RestartSec=15s

                [Install]
                WantedBy=multi-user.target
                EOF
            }]
          }
        })

        connection = {
          type     = "ssh"
          username = "core"
        }
      }
    }
  }
}
