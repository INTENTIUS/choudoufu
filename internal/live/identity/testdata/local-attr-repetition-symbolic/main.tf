# #213's safety boundary: the key-set fix (#178, staticForEachKeys) expands
# aws_iam_group.team from local.members' KEYS alone, because a VALUE in
# local.members is a managed resource's attribute (not statically known
# without reading the cloud). [expansion.scope]'s keyOnly branch therefore
# hands this instance only each.key - each.value is left unset on purpose,
# not a value nothing here happens to have populated yet.
#
# local.suffix, reached transitively the same way local-attr-repetition's
# local.username is, reads each.value directly. This must still refuse
# cleanly - "Dynamic value in static context" - never fabricate an answer
# from the one value that IS known (each.key). Silently reusing each.key
# for each.value here is exactly the "bound to the key on both sides"
# failure the #213 brief calls out: it would make a resource depending on
# the wrong attribute look sound.
#
# The block is aws_iam_group, not aws_iam_user as this fixture read before
# GitHub issue #289: aws_iam_user is taggable and enumerable, so its own
# marker fallback would now answer this refusal too, silently deferring to
# the marker rather than raising an error - a safe outcome (no wrong value
# is ever used) but not what this fixture pins, which is the refusal
# ITSELF holding, ungated. aws_iam_group has no tags argument.

resource "aws_iam_group" "admins" {
  name = "admins"
}

locals {
  members = {
    "alice" = [aws_iam_group.admins.name],
  }
  suffix = "user-${each.value}"
}

resource "aws_iam_group" "team" {
  for_each = local.members
  name     = local.suffix
}
