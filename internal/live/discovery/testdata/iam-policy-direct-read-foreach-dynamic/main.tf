# Regression fixture for GitHub issue #1049's other half: a for_each whose
# instance KEYS are real and known (identity.Resolve derives them from the
# parent block's own expansion - see
# internal/live/identity/testdata/managed-read-bare, this fixture's model)
# but whose collection is not itself statically known from configuration
# alone: `aws_subnet.this` is a managed-resource reference, not a var/local/
# path/terraform expression, so [staticeval.ForEachElements] refuses it.
#
# [instanceRepetitionData] must refuse this the same way even though
# aws_iam_policy.dynamic_policy["a"] and ["b"] are both real, addressable
# instances - the composer cannot vouch for a collection it cannot itself
# evaluate, only for one it can.

resource "aws_subnet" "this" {
  for_each   = toset(["a", "b"])
  cidr_block = "10.0.0.0/24"
}

resource "aws_iam_policy" "dynamic_policy" {
  for_each = aws_subnet.this
  name     = "policy-${each.key}-policy"
  policy   = jsonencode({ Sid = "team" })
}
