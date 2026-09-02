# GitHub issue #221's proper fix, chain case: aws_cloudwatch_log_stream.child
# is a MIXED intermediate hop - log_group_name cascades from the log
# group's eligible data-source read, but "name" has no value at all, an
# independent hard failure (the same shape as ../cascade-multicomponent-
# blocked). aws_cloudwatch_log_subscription_filter.grandchild then
# references child's own log_group_name attribute, one hop further down
# the chain.
#
# child itself must stay hard-refused (it has a hard failure of its own),
# and grandchild's cascade onto child must ALSO stay hard-refused - never
# reclassified - because child never enters eligibleAddrs. If it did (the
# defect the interim fix's per-cascade-only accounting would have allowed
# had it not also failed conservatively on real-component count), the
# grandchild would read "no configuration edit is needed" while depending
# on an instance whose identity can never actually be built.
data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_cloudwatch_log_group" "p" {
  name = "/zones/${data.aws_route53_zone.primary.zone_id}"
}

resource "aws_cloudwatch_log_stream" "child" {
  log_group_name = aws_cloudwatch_log_group.p.name
  # "name" deliberately omitted - required, no value at all. Makes child a
  # mixed intermediate hop.
}

resource "aws_cloudwatch_log_subscription_filter" "grandchild" {
  log_group_name = aws_cloudwatch_log_stream.child.log_group_name
  name            = "grandchild-filter"
}
