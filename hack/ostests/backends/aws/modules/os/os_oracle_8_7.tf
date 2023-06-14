# https://blogs.oracle.com/linux/post/running-oracle-linux-in-public-clouds
# https://forums.oracle.com/ords/apexds/post/launch-an-oracle-linux-instance-in-aws-9462

data "aws_ami" "oracle_8_7" {
  count = var.os == "oracle_8_7" ? 1 : 0

  owners      = ["131827586825"]
  name_regex  = "^OL8\\.7-x86_64-HVM-"
  most_recent = true

  filter {
    name   = "name"
    values = ["OL8.7-x86_64-HVM-*"]
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
  os_oracle_8_7 = var.os != "oracle_8_7" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.oracle_8_7.*.id)

        # https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/install-CloudWatch-Agent-commandline-fleet.html
        user_data = var.cloudwatch_agent_config == null ? null : format("#cloud-config\n%s", jsonencode({
          write_files = [{
            path    = "/etc/k0s-ostests/cloudwatch-agent.json"
            content = jsonencode(var.cloudwatch_agent_config)
          }]

          runcmd = [
            "curl -sSLo amazon-cloudwatch-agent.rpm https://s3.amazonaws.com/amazoncloudwatch-agent/oracle_linux/amd64/latest/amazon-cloudwatch-agent.rpm",
            "rpm -U ./amazon-cloudwatch-agent.rpm",
            "rm ./amazon-cloudwatch-agent.rpm",
            "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c file:/etc/k0s-ostests/cloudwatch-agent.json",
            "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a start",
          ]
        })),

        connection = {
          type     = "ssh"
          username = "ec2-user"
        }
      }
    }
  }
}
