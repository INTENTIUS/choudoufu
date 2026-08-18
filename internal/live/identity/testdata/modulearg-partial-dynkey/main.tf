# The mutation of modulearg-partial: the dynamic part is a KEY rather than a
# value. Nothing here says which instances exist, so the rebuild must refuse
# the whole argument rather than name one of the two instances and invent the
# other - a wrong marker where the poisoned refusal was merely a missing one.
resource "aws_iam_role" "r" {
  name = "the-role"
}

module "u" {
  source = "./mod"

  enabled = true

  users = {
    (aws_iam_role.r.arn) = { note = "keyed by something no run can name yet" }
    bob                  = { note = "literal" }
  }
}
