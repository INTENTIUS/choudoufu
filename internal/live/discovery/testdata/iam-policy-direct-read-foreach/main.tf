# Regression fixture for GitHub issue #1049: a for_each-expanded
# aws_iam_policy whose `name` argument reads each.value, over a set literal
# [staticeval.ForEachElements] can evaluate in full. each.key and each.value
# are the same string for a set-backed for_each, so this also exercises
# each.key indirectly - a map-keyed for_each would need a distinct value,
# which testdata/iam-policy-direct-read-foreach-dynamic's "a" -> parent
# instance case does not give us either, so this fixture is the one place
# each.value is actually read back through [composeIAMPolicyARN].

resource "aws_iam_policy" "team_policy" {
  for_each = toset(["ops", "dev"])
  name     = "team-${each.value}-policy"
  policy   = jsonencode({ Sid = "team" })
}
