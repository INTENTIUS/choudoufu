# Component.OmitIfAbsent, exercised through aws_lambda_permission's own
# qualifier - the archetype the field's doc comment names. The provider (aws
# 6.59.0) documents two import forms in the same Import section:
#
#	my_test_lambda_function/AllowExecutionFromCloudWatch
#	my_test_lambda_function:qualifier_name/AllowExecutionFromCloudWatch
#
# unqualified has no qualifier and no colon segment at all - it is not that
# the segment is present and empty. qualified has both. The two resources
# below reuse the provider doc's own example values so the rendered
# ImportID can be checked against the documented string directly, not just
# against this test's own expectation.
resource "aws_lambda_permission" "unqualified" {
  action        = "lambda:InvokeFunction"
  function_name = "my_test_lambda_function"
  principal     = "events.amazonaws.com"
  statement_id  = "AllowExecutionFromCloudWatch"
}

resource "aws_lambda_permission" "qualified" {
  action        = "lambda:InvokeFunction"
  function_name = "my_test_lambda_function"
  principal     = "events.amazonaws.com"
  statement_id  = "AllowExecutionFromCloudWatch"
  qualifier     = "qualifier_name"
}

# Absence is not only syntactic. terraform-aws-modules' own S3 notification
# module (.corpus/s3-bucket/modules/notification/main.tf:72) writes
# `qualifier = try(each.value.qualifier, null)`: the argument line IS
# present, so it is not what [attr == nil] means, but this entry's map has
# no "qualifier" key, so the expression statically evaluates to null all
# the same. That must resolve exactly like unqualified above, not raise
# "Null identity argument".
variable "notifications" {
  type = map(object({
    function_name = string
    qualifier     = optional(string)
  }))
  default = {
    from_map = { function_name = "my_test_lambda_function" }
  }
}

resource "aws_lambda_permission" "present_but_null" {
  for_each      = var.notifications
  action        = "lambda:InvokeFunction"
  function_name = each.value.function_name
  principal     = "events.amazonaws.com"
  statement_id  = "AllowFromMapNotification"
  qualifier     = try(each.value.qualifier, null)
}
