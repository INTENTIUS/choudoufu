# GitHub issue #221's proper fix, for_each case: aws_cloudwatch_log_stream
# is for_each'd over two keys. Both instances' log_group_name cascades from
# the same log group's eligible data-source read. Only key "b"'s "name"
# evaluates to null (a per-instance, independent hard failure - "Null
# identity argument", internal/live/identity/resolve.go's stringValue);
# key "a"'s "name" is an ordinary literal and never fails at all.
#
# Only child["b"] is a mixed instance. hardFailureAddrs and eligibleAddrs
# are keyed by the full instance address, key included
# (aws_cloudwatch_log_stream.child["a"] vs ["b"]), so child["a"]'s cascade
# must reclassify as data-read-eligible while child["b"]'s stays hard
# refused - the two keys of one resource block must not share a verdict.
locals {
  names = {
    a = "stream-a"
    b = null
  }
}

data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_cloudwatch_log_group" "p" {
  name = "/zones/${data.aws_route53_zone.primary.zone_id}"
}

resource "aws_cloudwatch_log_stream" "child" {
  for_each        = local.names
  log_group_name  = aws_cloudwatch_log_group.p.name
  name            = each.value
}
