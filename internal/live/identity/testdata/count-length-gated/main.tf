# count = <var> ? length(<resource>) : <literal>: length(<resource>)
# sits in a ternary's true branch, gated by a flag that has nothing to do
# with the resource it counts. mgmt_nessus_ec2.tf and mgmt_bastion_ec2.tf in
# cyhy-amis use exactly this shape to gate an optional management VPC's
# resources on both a feature flag and a sibling resource's own instance
# count.
resource "aws_eip" "pool" {
  count = 3
}

variable "enabled" {
  type    = bool
  default = true
}

resource "aws_cloudwatch_log_group" "gated" {
  count = var.enabled ? length(aws_eip.pool) : 0

  name = "/estate/log-${count.index}"
}
