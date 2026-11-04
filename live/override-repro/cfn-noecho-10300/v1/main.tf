resource "aws_cloudformation_stack" "s" {
  name = "${var.sfx}-stack"
  parameters = { Secret = "hunter2-not-real" }
  template_body = jsonencode({
    Parameters = { Secret = { Type = "String", NoEcho = true } }
    Resources  = { H = { Type = "AWS::CloudFormation::WaitConditionHandle" } }
  })
}
