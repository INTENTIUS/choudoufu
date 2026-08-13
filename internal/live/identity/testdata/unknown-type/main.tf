# A resource type with no entry in the v0 identity table. Resolution must
# refuse it by name rather than assume anything about its identity.
# aws_customer_gateway held this fixture's place until the EC2 networking
# batch (issue #65) admitted it.
resource "aws_cloudwatch_event_rule" "app" {
  name                = "example-rule"
  schedule_expression = "rate(5 minutes)"
}
