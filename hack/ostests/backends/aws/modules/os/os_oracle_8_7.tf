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

        user_data = format("#cloud-config\n%s", jsonencode({
          write_files = var.cloudwatch_agent_config == null ? [] : [{
            path    = "/etc/k0s-ostests/cloudwatch-agent.json"
            content = jsonencode(var.cloudwatch_agent_config)
          }]

          runcmd = concat(
            # https://docs.k0sproject.io/v1.27.2+k0s.0/networking
            [
              "firewall-cmd --zone=public --add-port=179/tcp --permanent",   # kube-router
              "firewall-cmd --zone=public --add-port=2380/tcp --permanent",  # etcd peers
              "firewall-cmd --zone=public --add-port=4789/tcp --permanent",  # calico
              "firewall-cmd --zone=public --add-port=6443/tcp --permanent",  # kube-apiserver
              "firewall-cmd --zone=public --add-port=8132/tcp --permanent",  # konnectivity
              "firewall-cmd --zone=public --add-port=9443/tcp --permanent",  # k0s-api
              "firewall-cmd --zone=public --add-port=10250/tcp --permanent", # kubelet
              "firewall-cmd --reload",
            ],
            # https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/install-CloudWatch-Agent-commandline-fleet.html
            var.cloudwatch_agent_config == null ? [] : [
              "curl -sSLo amazon-cloudwatch-agent.rpm https://s3.amazonaws.com/amazoncloudwatch-agent/oracle_linux/amd64/latest/amazon-cloudwatch-agent.rpm",
              "rpm -U ./amazon-cloudwatch-agent.rpm",
              "rm ./amazon-cloudwatch-agent.rpm",
              "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a fetch-config -m ec2 -c file:/etc/k0s-ostests/cloudwatch-agent.json",
              "/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a start",
            ],
          )
        })),

        connection = {
          type     = "ssh"
          username = "ec2-user"
        }
      }
    }
  }
}
