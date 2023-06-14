locals {
  cloudwatch_extra_dimensions = !var.cloudwatch_enabled ? null : merge(var.additional_tags, {
    "ostests.k0sproject.io/instance" = local.resource_name_prefix
    "ostests.k0sproject.io/os"       = var.os
  })

  # https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/CloudWatch-Agent-Configuration-File-Details.html
  cloudwatch_agent_config = !var.cloudwatch_enabled ? null : {
    agent = { metrics_collection_interval = 10 }
    metrics = {
      append_dimensions = {
        ImageId      = "$${aws:ImageId}"
        InstanceId   = "$${aws:InstanceId}"
        InstanceType = "$${aws:InstanceType}"
      }

      metrics_collected = {
        cpu = {
          append_dimensions = local.cloudwatch_extra_dimensions
          measurement = [
            "time_idle", "time_iowait", "time_system", "time_user",
            "usage_idle", "usage_iowait", "usage_system", "usage_user",
          ]
          resources = ["*"]
          totalcpu  = false
        }

        disk = {
          append_dimensions = local.cloudwatch_extra_dimensions
          measurement       = ["free", "inodes_free"],

          drop_device = true,
          ignore_file_system_types = [
            "autofs",
            "bpf",
            "cgroup2", "configfs",
            "debugfs", "devpts", "devtmpfs",
            "fusectl",
            "hugetlbfs",
            "mqueue",
            "proc", "pstore",
            "securityfs", "sysfs",
            "tracefs",
          ]
        },

        diskio = {
          append_dimensions = local.cloudwatch_extra_dimensions
          measurement = sort([
            "io_time",
            "reads", "writes",
            "read_bytes", "write_bytes",
          ])
        }

        mem = {
          append_dimensions = local.cloudwatch_extra_dimensions
          measurement = [
            "active", "available", "available_percent",
            "buffered",
            "cached",
            "free",
            "inactive",
            "total",
            "used", "used_percent",
          ]
        }

        netstat = {
          append_dimensions = local.cloudwatch_extra_dimensions
          measurement = [
            "tcp_established",
            "tcp_time_wait"
          ]
        }

        processes = {
          append_dimensions = local.cloudwatch_extra_dimensions
          measurement = [
            "blocked",
            "dead",
            "idle",
            "paging",
            "running",
            "sleeping", "stopped",
            "total", "total_threads",
            "wait",
            "zombies",
          ]
        }
      }
    }
  }
}
