# Limits fixture: RuleUnadmittedType.
#
# aws_nat_gateway held this fixture's place until the EC2 networking batch
# (issue #65) admitted it, and aws_cloudwatch_event_rule until the
# omitted-bus fallback vocabulary ([Component.Default]) let its batch land
# (#175). aws_iam_access_key is stabler than either: one of
# live/SURVEY.md's curated 68 top types (TestLimitationsDocAgainstSurvey
# requires the example to stay in that roster), and excluded by ruling
# rather than by a gap - the access key ID is server-assigned and the
# secret half is unreadable after create, so #125 ruled the ops exclusion
# stands and the one prior admission was withdrawn. The standing credential
# exclusion is the single class of type parity deliberately leaves out, so
# no future ratification batch retires this example.

resource "aws_iam_access_key" "web" {
  user = "example-user"
}
