# Fixture: one receipt plus three ways of illegally depending on it — a
# plain resource argument, a depends_on entry, and an output — each expected
# to trip RuleReceiptLeaf exactly once. See stateless/RECEIPTS.md, "Guard 4
# — the leaf rule". An ordinary aws_ssm_parameter (not under the
# /tofu-receipts/ naming convention) is included too, to prove the rule
# checks the naming convention rather than the type alone.

resource "aws_ssm_parameter" "demo_effect" {
  name  = "/tofu-receipts/stateless-e2e/demo-effect"
  type  = "String"
  value = sha256(jsonencode({ input = "x" }))
}

resource "aws_ssm_parameter" "ordinary_param" {
  name  = "/app/config/flag"
  type  = "String"
  value = "on"
}

resource "aws_s3_bucket" "leaks_via_argument" {
  bucket = aws_ssm_parameter.demo_effect.value
}

resource "aws_s3_bucket" "reads_ordinary_param" {
  bucket = aws_ssm_parameter.ordinary_param.value
}

resource "aws_iam_role" "leaks_via_depends_on" {
  name = "leaks-via-depends-on"

  assume_role_policy = "{}"

  depends_on = [aws_ssm_parameter.demo_effect]
}

output "leaks_via_output" {
  value = aws_ssm_parameter.demo_effect.arn
}
