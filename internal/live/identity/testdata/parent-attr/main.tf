# A child reads one attribute of a parent whose import identity is several
# attributes joined by a separator. This is the shape eight of the corpus's
# published deployments are blocked on and it is the ordinary EventBridge
# idiom: a target names its rule by the rule's own name.
#
# The rule's identity is EVENT_BUS_NAME/NAME, so "name" is not the whole of
# it - which is why the whole import ID is the wrong answer to
# aws_cloudwatch_event_rule.nightly.name, and why the entry's own components
# have to be what answers it.

resource "aws_cloudwatch_event_rule" "nightly" {
  event_bus_name      = "ops-bus"
  name                = "nightly-sweep"
  schedule_expression = "rate(1 day)"
}

resource "aws_cloudwatch_event_target" "nightly" {
  event_bus_name = "ops-bus"
  rule           = aws_cloudwatch_event_rule.nightly.name
  target_id      = "sweep"
  arn            = "arn:aws:lambda:us-east-1:000000000000:function:sweep"
}
