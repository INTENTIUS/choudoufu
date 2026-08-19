# terraform-aws-modules/security-group v6.0.0's own composition, reduced to
# the two things that make it different from modulearg-partial.
#
# First, the partial argument has to cross TWO module calls, not one: the
# caller hands `refs` to ./preset, and ./preset hands what it built out of
# it to ./inner, whose for_each is the thing that has to expand.
#
# Second, the second call's argument is not a constructor at all - it is
# `merge(local.combined, var.extra_rules)`, exactly the way the preset
# submodules in that module compose a rule map. A rebuild that only ever
# rewrites an object or tuple constructor has nothing to rewrite there.
#
# The key set is stated outright in this file, all the way down: two preset
# names in ./preset's own default and one key here, so ./inner has exactly
# the four instances http/app, https/app, plus the two `extra_rules` keys.
# Only the VALUE under `app` is unknowable.
resource "aws_iam_role" "r" {
  name = "the-role"
}

module "preset" {
  source = "./preset"

  refs = {
    app = aws_iam_role.r.arn
  }
}
