# Issue #301: the terraform-aws-modules/iam "attach N policies to a role"
# shape. The for_each KEY SET is a literal object key ("ImageBuilder"), which
# #178's key-set fix already proves without evaluating a single value - but
# the one value is a bare reference to a SIBLING managed resource's
# server-assigned attribute, reached through a module-call argument whose
# declared type is a plain map(string), not through a for-comprehension
# (module-foreach-keyonly-value's shape) and not read with a trailing
# .attribute (module-foreach-var's shape). The child module's own resource
# then reads that map with for_each = var.policies and policy_arn =
# each.value - a BARE each.value, the one shape #260's eachValueSelect never
# reached because #251's declared-type conversion (typedvar.go) used to drop
# the pre-conversion expression the moment the module variable had any
# concrete declared type at all.
resource "aws_iam_policy" "imagebuilder" {
  name   = "image-builder"
  policy = "{}"
}

module "attach" {
  source = "./attach"

  role_name = "gh-image-builder"
  policies = {
    ImageBuilder = aws_iam_policy.imagebuilder.arn
  }
}
