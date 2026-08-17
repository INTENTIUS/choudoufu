# #260: one dynamic attribute inside a for_each element must not refuse the
# literal siblings sitting beside it. `account` is a data-source read, so the
# element does not evaluate as a value at all - but `name` two characters
# away is a string literal, and it is the only attribute the identity reads.
data "aws_caller_identity" "current" {}

module "u" {
  source = "./mod"

  users = {
    alice = { name = "alice-user", account = data.aws_caller_identity.current.account_id }
    bob   = { name = "bob-user", account = data.aws_caller_identity.current.account_id }
  }
}
