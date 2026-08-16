# Read-level counterpart to testdata/dynamic-block-iterator: proves the
# ACTUAL decode (configs.StaticEvaluator.DecodeBlock's dynblock expansion),
# not merely that eligibility analysis models the shape correctly.
# Eligibility and the real decode are two different code paths
# (analyze.go's own hand-rolled walk versus hcldec against the provider's
# real schema); getting the first right without the second would leave the
# 2 real corpus configs eligible but failing at read time.
locals {
  actions = ["a", "b"]
}

data "test_policy" "doc" {
  dynamic "statement" {
    for_each = local.actions
    content {
      sid = "generic-${statement.value}"
    }
  }
}

resource "aws_cloudwatch_log_group" "per_policy" {
  name = "/policies/${data.test_policy.doc.id}"
}
