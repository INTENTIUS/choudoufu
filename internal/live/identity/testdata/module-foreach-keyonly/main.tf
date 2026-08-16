# A module call's for_each whose KEYS are literal but whose VALUES are not:
# the paired object mentions a data source, which no static read can answer.
# Only each.key reaches an identity here, so every instance's identity is
# knowable before anything is read from the cloud even though each.value is
# not - the split staticForEachKeyNames exists to make.
data "aws_caller_identity" "current" {}

module "user" {
  source = "./user"

  for_each = {
    alice = { trusted_arn = data.aws_caller_identity.current.arn }
    bob   = { trusted_arn = data.aws_caller_identity.current.arn }
    carol = { trusted_arn = data.aws_caller_identity.current.arn }
  }

  name = each.key
}
