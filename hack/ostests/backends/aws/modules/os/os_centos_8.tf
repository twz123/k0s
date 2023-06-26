# https://centos.org/download/aws-images/

data "aws_ami" "centos_8" {
  count = var.os == "centos_8" ? 1 : 0

  owners      = ["125523088429"]
  name_regex  = "^CentOS Stream 8 x86_64 \\d+"
  most_recent = true

  filter {
    name   = "name"
    values = ["CentOS Stream 8 x86_64 *"]
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
  os_centos_8 = var.os != "centos_8" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.centos_8.*.id)

        user_data = var.cloudwatch_agent_config == null ? null : format("#cloud-config\n%s", jsonencode({
          write_files = [{
            path    = "/etc/k0s-ostests/cloudwatch-agent.json"
            content = jsonencode(var.cloudwatch_agent_config)
          }]

          # https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/install-CloudWatch-Agent-commandline-fleet.html
          runcmd = [
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
