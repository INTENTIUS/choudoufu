# #190: an Optional identity argument whose own Argument Reference bullet
# promises the provider assigns it a fresh value when the configuration
# omits it ("If omitted, Terraform will assign a random, unique name" and
# its siblings - see Component.ServerAssignedIfAbsent, populated by
# tools/row-gen/emit.go's mergeServerAssigned from live/import-grammar.json).
# Neither resource below sets its identity argument, and neither carries a
# *_prefix sibling (the different convention nameprefix_test.go covers), so
# this exercises the new branch in resolver.identityArgs on its own.
#
# A sibling instance that DOES set the argument must stay CONCRETE: the rule
# is per-instance, the same way the *_prefix convention is.
resource "aws_iam_role_policy" "omitted" {
  role   = "example-role"
  policy = "{}"
}

resource "aws_iam_role_policy" "named" {
  role   = "example-role"
  name   = "explicit-policy-name"
  policy = "{}"
}

resource "aws_lambda_permission" "omitted" {
  action        = "lambda:InvokeFunction"
  function_name = "example-function"
  principal     = "events.amazonaws.com"
}
