# https://alt.fedoraproject.org/cloud/

data "aws_ami" "fedora_38" {
  count = var.os == "fedora_38" ? 1 : 0

  owners      = ["125523088429"]
  name_regex  = "^Fedora-Cloud-Base-38-.+\\.x86_64-hvm-"
  most_recent = true

  filter {
    name   = "name"
    values = ["Fedora-Cloud-Base-38-*.x86_64-hvm-*"]
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
  os_fedora_38 = var.os != "fedora_38" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.fedora_38.*.id)

        # https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/install-CloudWatch-Agent-commandline-fleet.html
        user_data = var.cloudwatch_agent_config == null ? null : format("#cloud-config\n%s", jsonencode({
          write_files = [{
            path    = "/etc/k0s-ostests/cloudwatch-agent.json"
            content = jsonencode(var.cloudwatch_agent_config)
          }]

          runcmd = [
            # There's no specific RPM for Fedora. Go on and use Red Hat.
            "curl -sSLo amazon-cloudwatch-agent.rpm https://s3.amazonaws.com/amazoncloudwatch-agent/redhat/amd64/latest/amazon-cloudwatch-agent.rpm",
            "rpm -U ./amazon-cloudwatch-agent.rpm",
            "rm ./amazon-cloudwatch-agent.rpm",
            "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c file:/etc/k0s-ostests/cloudwatch-agent.json",
            "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a start",
          ]
        })),

        connection = {
          type     = "ssh"
          username = "fedora"
        }
      }
    }
  }
}
