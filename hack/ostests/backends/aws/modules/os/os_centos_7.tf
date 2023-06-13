# https://centos.org/download/aws-images/

data "aws_ami" "centos_7" {
  count = var.os == "centos_7" ? 1 : 0

  owners      = ["125523088429"]
  name_regex  = "^CentOS Linux 7 x86_64 - "
  most_recent = true

  filter {
    name   = "name"
    values = ["CentOS Linux 7 x86_64 - *"]
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
  os_centos_7 = var.os != "centos_7" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.centos_7.*.id)

        user_data = format("#cloud-config\n%s", jsonencode({
          bootcmd = [
            "rm /etc/machine-id",
            "systemd-machine-id-setup",
          ]

          write_files = var.cloudwatch_agent_config == null ? [] : [{
            path    = "/etc/k0s-ostests/cloudwatch-agent.json"
            content = jsonencode(var.cloudwatch_agent_config)
          }]

          # https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/install-CloudWatch-Agent-commandline-fleet.html
          runcmd = var.cloudwatch_agent_config == null ? [] : [
            "curl -sSLo amazon-cloudwatch-agent.rpm https://s3.amazonaws.com/amazoncloudwatch-agent/centos/amd64/latest/amazon-cloudwatch-agent.rpm",
            "rpm -U ./amazon-cloudwatch-agent.rpm",
            "rm ./amazon-cloudwatch-agent.rpm",
            "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c file:/etc/k0s-ostests/cloudwatch-agent.json",
            "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a start",
          ]
        })),

        connection = {
          type     = "ssh"
          username = "centos"
        }
      }
    }
  }
}
