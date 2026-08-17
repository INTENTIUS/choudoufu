# The other direction of the try() hazard, and the one that is a false
# refusal today: `name` is genuinely not in the element, the variable's
# declared type is `any` so no optional() default supplies it, and stock
# OpenTofu takes the fallback. This is terraform-aws-modules/vpc's
# vpc-endpoints shape, where try(each.value.protocol, "tcp") reads elements
# that carry only description and cidr_blocks.
data "aws_caller_identity" "current" {}

module "u" {
  source = "./mod"

  users = {
    alice = { description = "just a description", account = data.aws_caller_identity.current.account_id }
  }
}
