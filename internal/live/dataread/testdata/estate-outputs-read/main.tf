# GitHub issue #1575: a terraform_estate_outputs value in an identity-bearing
# argument. The builtin terraform provider manages nothing live (its only
# managed type, terraform_data, is logical), so without the cross-stack
# exemption the provider boundary refuses this source before the plan.
data "terraform_estate_outputs" "network" {
  estate = "network"
  names  = ["vpc_id"]
}

resource "aws_cloudwatch_log_group" "per_network" {
  name = "/networks/${data.terraform_estate_outputs.network.values.vpc_id}"
}
