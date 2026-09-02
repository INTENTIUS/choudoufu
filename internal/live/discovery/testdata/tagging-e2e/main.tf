# Type choice, recorded per the issue's instructions.
#
# aws_iam_role: taggable, mapped (live/mapping.json, via "name" ->
# AWS::IAM::Role), admitted in identity.DefaultTable, and has a row in the
# ARN join table (internal/live/discovery/tagging.go's arnJoinTable["iam"])
# that resolves unambiguously - iam:role/NAME, no id-shape disambiguation
# needed the way elasticloadbalancing's "loadbalancer" segment does. It is
# also universally supported: unlike EFS (testdata/cloudcontrol-e2e/main.tf's
# own opening comment) or Glue, IAM is one of the oldest and most complete
# services every AWS emulator implements, so a role created here is the
# lowest-risk real resource to stand this fixture up around.
#
# tagging_live_test.go's TestTaggingSweepAgainstFloci runs this fixture
# against real floci and skips with recorded evidence rather than asserting
# a bind floci cannot currently deliver: the Resource Groups Tagging API
# does not yet reflect resources tagged through the ordinary service APIs
# (the same gap class testdata/cloudcontrol-e2e/main.tf documents for Cloud
# Control's ListResources, not a different one). See that test's own doc
# comment for the full probe notes.

resource "aws_iam_role" "demo" {
  name = "tagging-e2e-demo"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })

  tags = {
    tofu-estate  = "tagging-e2e"
    tofu-address = "aws_iam_role.demo"
  }
}
