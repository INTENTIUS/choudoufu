# #260's try() hazard, the direction where try must NOT win. The attribute is
# present and it names a managed resource whose identity this package can
# resolve, so the identity is the ROLE's name. A fix that bound a partial
# each.value would drop the attribute, try would catch the resulting error,
# and "fallback" would be written into a cloud tag as this user's identity.
resource "aws_iam_role" "r" {
  name = "the-role"
}

data "aws_caller_identity" "current" {}

module "u" {
  source = "./mod"

  users = {
    alice = { name = aws_iam_role.r.name, account = data.aws_caller_identity.current.account_id }
  }
}
