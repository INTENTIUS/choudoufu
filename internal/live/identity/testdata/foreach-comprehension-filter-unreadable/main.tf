# The other half of the "if" clause rule: the filter reads the value side,
# and the source collection's values come from a managed resource, so
# nothing here can decide which elements survive it. The key CLAUSE would
# evaluate fine - it is the bare loop key - which is precisely the trap: a
# provable key expression over an unprovable membership test still yields
# an unprovable key set.
#
# Refusing the whole comprehension is the only sound answer. Treating the
# filter as though it passed everything would enumerate instances OpenTofu
# does not create.

resource "aws_iam_role" "team" {
  for_each = toset(["a", "b"])

  name = "team-${each.key}"
}

locals {
  users = {
    a = { role = aws_iam_role.team["a"].name }
    b = { role = aws_iam_role.team["b"].name }
  }

  merged = {
    for k, v in local.users : k => v if v.role != "team-a"
  }
}

resource "aws_iam_user" "this" {
  for_each = local.merged
  name     = each.key
}
