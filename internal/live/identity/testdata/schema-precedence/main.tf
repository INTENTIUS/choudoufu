# Three admitted, config-identified, ratified types
# (tools/row-gen/schemafirst.go's own reproduced list at the commit this
# fixture was added) - two single-attribute (aws_iam_role, aws_s3_bucket)
# and one two-attribute, literal-joined composite
# (aws_iam_role_policy_attachment) - so TestSchemaPrecedenceMatchesRowByValue
# exercises both the plain-string route and the identity-object route a
# synthesized multi-attribute entry always takes.

resource "aws_iam_role" "example" {
  name = "example-role"
}

resource "aws_s3_bucket" "example" {
  bucket = "example-bucket"
}

resource "aws_iam_role_policy_attachment" "example" {
  role       = "example-role"
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}
