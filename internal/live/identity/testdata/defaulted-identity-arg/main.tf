# Component.Default's positive case: aws_cloudwatch_event_rule's identity is
# event_bus_name/name, and the provider documents that an omitted
# event_bus_name means the "default" event bus (the scraped
# omitted_fallbacks fact the table row carries as Default). A configuration
# that never writes the bus must resolve to default/<name>, not refuse -
# and one that does write it must win over the fallback.
resource "aws_cloudwatch_event_rule" "quiet" {
  name          = "capture-signin"
  event_pattern = "{\"detail-type\":[\"AWS Console Sign In via CloudTrail\"]}"
}

resource "aws_cloudwatch_event_rule" "loud" {
  name           = "capture-signin-custom"
  event_bus_name = "team-bus"
  event_pattern  = "{\"detail-type\":[\"AWS Console Sign In via CloudTrail\"]}"
}
