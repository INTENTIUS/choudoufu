# Fixture for the residue roster's unmapped cohort (issue #49).
# aws_accessanalyzer_archive_rule is outside the v0 table and, per
# live/mapping.json, a via:"none" row with no CloudFormation counterpart.

resource "aws_accessanalyzer_archive_rule" "rule" {
  analyzer_name = "example"
  rule_name     = "example"
}
