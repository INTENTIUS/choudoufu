# Regression fixture for GitHub issue #1046: two needs-discovery
# aws_iam_policy instances, declared with no static name so each one's
# identity can only be settled by listing the whole account (aws_iam_policy
# is ServerAssigned per internal/live/identity's table_generated.go, so
# there is no config-derived identity path to fall back on). "beyond" is
# built to sit past the fixture's simulated mid-list failure - see
# TestListTruncationDoesNotSilentlyProposeCreate in pagination_test.go.

resource "aws_iam_policy" "before" {
  policy = jsonencode({ Sid = "before" })
}

resource "aws_iam_policy" "beyond" {
  policy = jsonencode({ Sid = "beyond" })
}
