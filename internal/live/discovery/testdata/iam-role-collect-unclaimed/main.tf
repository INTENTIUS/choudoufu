# Regression fixture for GitHub issue #1136's SECOND path, the one the #1137
# worker found from the other side by running corpus-ec2-instance-complete.
#
# One needs-discovery aws_iam_role instance, declared with no static name, so
# the config-driven scan lists the account for it. iam:ListRoles returns no
# tags, and aws_iam_role is in taggingAPIUnservedServices ("aws_iam_"), so
# #266's tag-index rescue cannot answer either - which means the estate's own
# role, correctly stamped by internal/live/stamp, comes back looking
# unmarked.
#
# With CollectUnclaimed set, scanType does NOT drop such an object the way an
# ordinary sweep does: it keeps it and files it in Result.Unclaimed, where
# internal/live/foreign reported it FOREIGN - the run telling an operator
# that a resource their own estate owns and tagged is an unowned stray.

resource "aws_iam_role" "this" {
  assume_role_policy = jsonencode({})
}
