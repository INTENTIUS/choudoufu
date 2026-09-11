# Regression fixture for GitHub issue #1046's direct-read fallback: one
# aws_iam_policy instance whose `name` argument is a plain string literal,
# so its live ARN can be composed from configuration alone
# (arn:aws:iam::ACCOUNT:policy/NAME, default path) the moment this run
# knows the account ID from its own listing - see directread.go's
# composeIAMPolicyARN. aws_iam_policy is still ServerAssigned per
# internal/live/identity's table_generated.go (the provider's identity
# schema wants the whole ARN as one opaque string), so this instance is
# ClassNeedsDiscovery exactly like testdata/iam-policy-pagination's - the
# only difference from that fixture is the static name this one needs.

resource "aws_iam_policy" "team_0002_policy" {
  name   = "team-0002-policy"
  policy = jsonencode({ Sid = "team" })
}
