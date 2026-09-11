# Regression fixture for GitHub issues #1037/#1039: one needs-discovery
# aws_iam_policy instance, declared with no static name so its identity can
# only be settled by listing the account (the same shape the config-driven
# scan already needs for binding). aws_iam_policy is in
# taggingAPIUnservedServices ("aws_iam_"), so sweepTypes() adds it back into
# the native sweep universe even though it is declared - see
# dedupAlreadyConfigScanned's own doc comment in discovery.go for why the
# native sweep must not list it a second time.

resource "aws_iam_policy" "owned" {
  policy = jsonencode({})
}
