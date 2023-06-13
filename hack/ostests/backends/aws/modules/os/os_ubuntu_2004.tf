# https://ubuntu.com/server/docs/cloud-images/amazon-ec2

data "aws_ami" "ubuntu_2004" {
  count = var.os == "ubuntu_2004" ? 1 : 0

  owners      = ["099720109477"]
  name_regex  = "ubuntu/images/hvm-ssd/ubuntu-focal-20.04-amd64-server-\\d+"
  most_recent = true

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd/ubuntu-focal-20.04-amd64-server-*"]
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
  os_ubuntu_2004 = var.os != "ubuntu_2004" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.ubuntu_2004.*.id)

        user_data = format("#cloud-config\n%s", jsonencode({
          write_files = var.cloudwatch_agent_config == null ? [] : [{
            path    = "/etc/k0s-ostests/cloudwatch-agent.json"
            content = jsonencode(var.cloudwatch_agent_config)
          }]

          # https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/install-CloudWatch-Agent-commandline-fleet.html
          runcmd = concat(
            ["find /etc/update-motd.d -maxdepth 1 -executable \\( -type f -o -type l \\) -exec chmod -x '{}' \\;"],
            var.cloudwatch_agent_config == null ? [] : [
              "wget https://s3.amazonaws.com/amazoncloudwatch-agent/ubuntu/amd64/latest/amazon-cloudwatch-agent.deb",
              "dpkg -i -E ./amazon-cloudwatch-agent.deb",
              "rm ./amazon-cloudwatch-agent.deb",
              "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c file:/etc/k0s-ostests/cloudwatch-agent.json",
              "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a start",
            ]
          )
        })),

        connection = {
          type     = "ssh"
          username = "ubuntu"
        }
      }
    }
  }
}
