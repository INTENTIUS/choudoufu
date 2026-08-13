# Fixture for the residue roster's deprecated-service cohort (issue #49).
# aws_pinpoint_app is outside the v0 table and, per live/residue.go's
# DeprecatedServices, in a service AWS is retiring.

resource "aws_pinpoint_app" "app" {
  name = "example"
}
