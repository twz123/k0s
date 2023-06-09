data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_ec2_instance_type_offerings" "in_available_azs" {
  for_each = toset(flatten(concat(
    var.controller_num_nodes < 1 ? [] : [local.node_configs.controller.instance_type],
    var.controller_worker_num_nodes < 1 ? [] : [local.node_configs["controller+worker"].instance_type],
    var.worker_num_nodes < 1 ? [] : [local.node_configs.worker.instance_type],
  )))

  filter {
    name   = "instance-type"
    values = [each.value]
  }

  filter {
    name   = "location"
    values = toset(data.aws_availability_zones.available.names)
  }

  location_type = "availability-zone"
}

resource "random_shuffle" "selected_az" {
  input        = setintersection([for k, v in data.aws_ec2_instance_type_offerings.in_available_azs : v.locations]...)
  result_count = 1
}
