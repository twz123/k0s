# FIXME cloudwatch agent

locals {
  # https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/install-CloudWatch-Agent-commandline-fleet.html
  cloudwatch_agent_debian_deb = "https://s3.amazonaws.com/amazoncloudwatch-agent/debian/amd64/latest/amazon-cloudwatch-agent.deb"

  cloudwatch_agent_config = {
    agent = {
      metrics_collection_interval = 60
      run_as_user                 = "root"
    },
    metrics = {
      metrics_collected = {
        disk = {
          measurement                 = ["used_percent"],
          metrics_collection_interval = 60
          resources                   = ["*"]
        },
        mem = {
          measurement                 = ["mem_used_percent"],
          metrics_collection_interval = 60
        }
      }
    }
  }
}
