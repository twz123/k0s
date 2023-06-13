# https://wiki.debian.org/Cloud/AmazonEC2Image/Buster

data "aws_ami" "debian_10" {
  count = var.os == "debian_10" ? 1 : 0

  owners      = ["136693071363"]
  name_regex  = "^debian-10-amd64-\\d+-\\d+$"
  most_recent = true

  filter {
    name   = "name"
    values = ["debian-10-amd64-*-*"]
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
  os_debian_10 = var.os != "debian_10" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.debian_10.*.id)

        user_data = format("#cloud-config\n%s", jsonencode({
          runcmd = concat(
            ["truncate -s0 /etc/motd"],

            # https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/install-CloudWatch-Agent-commandline-fleet.html
            var.cloudwatch_agent_config == null ? [] : [
              "wget https://s3.amazonaws.com/amazoncloudwatch-agent/debian/amd64/latest/amazon-cloudwatch-agent.deb",
              "dpkg -i -E ./amazon-cloudwatch-agent.deb",
              "rm ./amazon-cloudwatch-agent.deb",
              "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c file:/etc/k0s-ostests/cloudwatch-agent.json",
              "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a start",
            ],
          )

          write_files = var.cloudwatch_agent_config == null ? [] : [{
            path    = "/etc/k0s-ostests/cloudwatch-agent.json"
            content = jsonencode(var.cloudwatch_agent_config)
          }]
        })),

        connection = {
          type     = "ssh"
          username = "admin"
        }
      }
    }
  }
}
