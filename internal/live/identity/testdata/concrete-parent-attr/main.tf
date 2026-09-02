# A concrete parent read through a NON-identity attribute of its own.
#
# The shape is govuk-infrastructure's, twice over: terraform/deployments/
# release/assumed.tf:60 and terraform/deployments/synthetic-test/assumed.tf:50
# both write principal_arn = aws_iam_role.<x>.arn. aws_iam_role's identity
# is its name; "arn" is not in its IdentityAttrs and its own block never
# writes an arn argument, so neither Resolution.attrParts nor
# resolver.siblingLiteralExpr can answer, and before the concrete branch in
# resolver.parentPart this refused outright.
#
# The other two parents are here to show the rule is a property and not a
# list of three type names: aws_lambda_function (.corpus/cyhy-amis's
# fdi_lambda.tf) and aws_ecs_cluster (.corpus/ecs's cluster module) are the
# other parent types in the same family, and every one of them is
# client-named, so all three resolve CONCRETE with an arn nobody wrote down.

resource "aws_iam_role" "assumed" {
  name               = "release-assumed"
  assume_role_policy = "{}"
}

resource "aws_eks_access_entry" "assumed" {
  cluster_name  = "govuk"
  principal_arn = aws_iam_role.assumed.arn
}

resource "aws_lambda_function" "fdi" {
  function_name = "fdi-ingest"
  role          = "arn:aws:iam::123456789012:role/lambda"
}

resource "aws_lambda_permission" "fdi_events" {
  function_name = aws_lambda_function.fdi.arn
  statement_id  = "AllowEvents"
  action        = "lambda:InvokeFunction"
  principal     = "events.amazonaws.com"
}

resource "aws_ecs_cluster" "main" {
  name = "prod-cluster"
}

resource "aws_ecs_service" "web" {
  name    = "web"
  cluster = aws_ecs_cluster.main.arn
}
