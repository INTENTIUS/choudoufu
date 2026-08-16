# The other half of module-foreach-keyonly: the keys are still literal, but
# the child module reaches each.VALUE, which no static read can answer. The
# instances exist; their identities do not, and the refusal has to say so
# rather than invent one.
data "aws_caller_identity" "current" {}

module "user" {
  source = "./user"

  for_each = {
    alice = { trusted_arn = data.aws_caller_identity.current.arn }
    bob   = { trusted_arn = data.aws_caller_identity.current.arn }
  }

  name = each.value.trusted_arn
}
