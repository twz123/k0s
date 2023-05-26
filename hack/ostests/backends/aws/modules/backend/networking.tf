data "aws_vpc" "default" {
  default = true
}

data "aws_availability_zones" "available" {
  state = "available"
}

resource "random_integer" "az_offset" {
  min   = 1
  max   = length(data.aws_availability_zones.available.names) - 1
}

data "aws_subnet" "az_default" {
  vpc_id            = data.aws_vpc.default.id
  availability_zone = data.aws_availability_zones.available.names[random_integer.az_offset.id]
  default_for_az    = true
}

resource "aws_route_table_association" "az_default" {
  subnet_id      = data.aws_subnet.az_default.id
  route_table_id = data.aws_vpc.default.main_route_table_id
}

resource "aws_security_group" "all_access" {
  name   = "${var.resource_name_prefix}-all-access"
  vpc_id = data.aws_vpc.default.id

  tags = {
    Name = "${var.resource_name_prefix}-all-access"
  }
}

resource "aws_security_group_rule" "ingress_all_access" {
  description       = "Allow ALL traffic from the outside"
  security_group_id = aws_security_group.all_access.id
  type              = "ingress"
  from_port         = 0
  to_port           = 65535
  protocol          = "all"
  cidr_blocks       = ["0.0.0.0/0"]
}

resource "aws_security_group_rule" "egress_all_access" {
  description       = "Allow ALL traffic to the outside"
  security_group_id = aws_security_group.all_access.id
  type              = "egress"
  from_port         = 0
  to_port           = 65335
  protocol          = "all"
  cidr_blocks       = ["0.0.0.0/0"]
}
