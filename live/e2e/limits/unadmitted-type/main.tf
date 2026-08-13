# Limits fixture: RuleUnadmittedType.
#
# aws_nat_gateway held this fixture's place until the EC2 networking batch
# (issue #65) admitted it; aws_cloudwatch_event_rule takes over as a
# replacement stabler than "not yet wired" could offer. It is one of
# live/SURVEY.md's curated 68 top types (TestLimitationsDocAgainstSurvey
# requires the example to stay in that roster), and its own documented
# import id — "event_bus_name/rule_name", where event_bus_name silently
# defaults to the account's default bus when omitted from configuration —
# needs a [Component] this table's vocabulary does not have yet: a literal
# fallback for an omitted argument, not just a separator. That gap has
# already outlived four ratification batches (messaging, DynamoDB
# periphery, RDS, ECS/EKS all cite this exact type when explaining why they
# left a similarly-shaped composite unwired), so it is a stable pick rather
# than a type the very next batch is likely to reach. See
# internal/live/identity/table.go's messaging-batch comment for the full
# grammar citation and live/LIMITATIONS.md.

resource "aws_cloudwatch_event_rule" "web" {
  name                = "example-rule"
  schedule_expression = "rate(5 minutes)"
}
