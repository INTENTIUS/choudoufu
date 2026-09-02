# The same hazard with the attribute present and NOT resolvable: a
# data-source read, which is knowable at plan time and not before. try must
# not win here either - the attribute exists, so the language never reaches
# the fallback, and answering "fallback" would be a fabricated identity where
# a refusal belongs.
data "aws_iam_role" "d" {
  name = "looked-up"
}

data "aws_caller_identity" "current" {}

module "u" {
  source = "./mod"

  users = {
    alice = { name = data.aws_iam_role.d.name, account = data.aws_caller_identity.current.account_id }
  }
}
