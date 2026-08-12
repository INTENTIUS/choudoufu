# A resource reference passed through a function. The reference cannot be
# followed structurally and the function cannot be evaluated, so the
# identity is unresolvable.
resource "aws_iam_role" "app" {
  name = "estate-app"
}

resource "aws_cloudwatch_log_group" "app" {
  name = upper(aws_iam_role.app.name)
}
