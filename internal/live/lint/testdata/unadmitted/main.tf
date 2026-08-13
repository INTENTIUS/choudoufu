# Fixture for RuleUnadmittedType. aws_cloudwatch_event_rule is a real,
# curated-68 type this table's [Component] vocabulary cannot yet wire, in no
# residue cohort (TestRefusalSilentForTypeInNoCohort below relies on that).

resource "aws_cloudwatch_event_rule" "web" {
  name                = "example-rule"
  schedule_expression = "rate(5 minutes)"
}
