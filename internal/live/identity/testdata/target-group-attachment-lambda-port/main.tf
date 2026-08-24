# Component.OmitIfAbsent's port segment (#399, maintainer ruling
# 2026-08-24). botocore's elbv2 2015-12-01 model documents port as not
# applying to a Lambda-type target ("This parameter is not used if the
# target is a Lambda function"), and CreateTargetGroupInput.Port carries
# the identical caveat; a lambda target group holds exactly one target and
# no port, so two attachments differing only by port is structurally
# impossible for this shape - the collision OmitIfAbsent exists to guard
# against on every other optional component cannot occur here.
#
# lambda mirrors terraform-aws-modules/terraform-aws-alb's own
# local.lambda_target_groups shape (corpus-alb-complete's real estate):
# port is written but conditionally null for a lambda target, not merely
# absent from the block - the "component present, evaluates to a clean
# null" path [Component.OmitIfAbsent] must also redirect, not just
# [attr == nil].
variable "target_type" {
  type    = string
  default = "lambda"
}

resource "aws_lb_target_group_attachment" "lambda" {
  target_group_arn = "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/lambda-tg/abc123"
  target_id         = "arn:aws:lambda:us-east-1:123456789012:function:my-function"
  port              = var.target_type == "lambda" ? null : 8080
}

# instance mirrors the ordinary, unaffected shape: port present and
# non-null must keep rendering byte-identically to the pre-ruling behavior
# - this is the mutation boundary the fix must not cost.
resource "aws_lb_target_group_attachment" "instance" {
  target_group_arn = "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/inst-tg/def456"
  target_id         = "i-0123456789abcdef0"
  port              = 80
}
