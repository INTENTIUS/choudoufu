# The widest shape in the terraform-aws-modules examples: a composite module
# argument whose skeleton is entirely literal and one of whose leaves is not.
#
# `users` names two instances, keyed alice and bob, whatever
# aws_iam_role.r.arn turns out to be - the keys are written here. `groups` has
# two elements, whatever the second one turns out to be - the length is
# written here. Evaluating either argument as one expression makes the whole
# of it unavailable inside the module, so the child's for_each and count
# refuse over a key set and a length the configuration states outright.
resource "aws_iam_role" "r" {
  name = "the-role"
}

module "u" {
  source = "./mod"

  enabled = true

  users = {
    alice = { role = aws_iam_role.r.arn }
    bob   = { role = aws_iam_role.r.arn }
  }

  groups = ["literal-group", aws_iam_role.r.arn]
}
