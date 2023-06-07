resource "aws_iam_role" "k0s_node" {
  name = "${var.resource_name_prefix}-k0s-node"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
        Action = "sts:AssumeRole"
      }
    ]
  })
}

# FIXME missing privileges

# resource "aws_iam_instance_profile" "k0s_node" {
#   name = "${var.resource_name_prefix}-k0s-node"
#   role = aws_iam_role.k0s_node.name
# }

# // https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/create-iam-roles-for-cloudwatch-agent.html
# resource "aws_iam_role_policy_attachment" "k0s_node_cloudwatch_agent" {
#   role       = aws_iam_role.k0s_node.name
#   policy_arn = "arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy"
# }
