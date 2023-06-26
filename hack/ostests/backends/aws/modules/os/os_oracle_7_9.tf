# https://blogs.oracle.com/linux/post/running-oracle-linux-in-public-clouds
# https://forums.oracle.com/ords/apexds/post/launch-an-oracle-linux-instance-in-aws-9462

data "aws_ami" "oracle_7_9" {
  count = var.os == "oracle_7_9" ? 1 : 0

  owners      = ["131827586825"]
  name_regex  = "^OL7\\.9-x86_64-HVM-"
  most_recent = true

  filter {
    name   = "name"
    values = ["OL7.9-x86_64-HVM-*"]
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
  os_oracle_7_9 = var.os != "oracle_7_9" ? {} : {
    node_configs = {
      default = {
        ami_id = one(data.aws_ami.oracle_7_9.*.id)

        user_data = format("#cloud-config\n%s", jsonencode({
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
