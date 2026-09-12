# Regression fixture for GitHub issue #1049: a count-expanded aws_iam_policy
# whose `name` argument reads count.index. The collection (count = 3) is a
# plain literal, so [instanceRepetitionData] can bind count.index for any one
# instance and [composeIAMPolicyARN] can build that instance's own candidate
# ARN from configuration alone - exactly the terralith shape at scale: most
# of its IAM policies are count instances, not the single scalar instance
# testdata/iam-policy-direct-read covers.

resource "aws_iam_policy" "team_policy" {
  count  = 3
  name   = "team-${count.index}-policy"
  policy = jsonencode({ Sid = "team" })
}
