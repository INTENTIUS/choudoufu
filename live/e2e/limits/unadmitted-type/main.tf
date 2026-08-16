# Limits fixture: RuleUnadmittedType.
#
# aws_nat_gateway held this fixture's place until the EC2 networking batch
# (issue #65) admitted it, and aws_cloudwatch_event_rule until the
# omitted-bus fallback vocabulary ([Component.Default]) let its batch land
# (#175). aws_iam_access_key held it after those, and moved out when
# RuleMarkerlessType landed: it is on
# internal/live/identity.MarkerlessTypes, so the markerless-type refusal now
# claims it and this fixture would no longer report the rule it is named
# for.
#
# aws_acm_certificate_validation is the type left standing. It is one of
# live/SURVEY.md's curated top types (TestLimitationsDocAgainstSurvey
# requires the example to stay in that roster), it is unadmitted, and it is
# not on the markerless roster - it is a waiter rather than a resource, out
# by the ops ruling recorded in tools/survey-gen's opsExcluded, and no
# ratification batch retires it.

resource "aws_acm_certificate_validation" "web" {
  certificate_arn = "arn:aws:acm:us-east-1:123456789012:certificate/example"
}
