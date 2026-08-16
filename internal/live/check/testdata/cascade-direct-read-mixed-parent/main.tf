# The one shape ../cascade-chain-mixed-intermediate does NOT cover: the
# mixed hop enters eligibleAddrs through the DIRECT path rather than the
# cascade path.
#
# In the chain fixture the mixed hop's good component is itself a cascade,
# so the fixpoint's hardFailureAddrs gate sees it and keeps the hop out of
# eligibleAddrs. Here the mixed hop reads the data source directly, so
# [Analyze]'s classifyDataSite branch adds it to eligibleAddrs the moment
# the read is classified - a branch with no hard-failure gate of its own,
# because the two facts arrive from different diagnostics and either may be
# seen first.
#
# aws_cloudwatch_log_stream.parent is therefore both eligible (its
# log_group_name reads an eligible data source) and independently
# hard-failing ("name" has no value at all, so its identity can never be
# built). aws_cloudwatch_log_subscription_filter.dependent's cascade onto it
# must stay a hard "Unresolvable identity" refusal: the fixpoint has to
# check the PARENT for a hard failure, not only the child.
data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_cloudwatch_log_stream" "parent" {
  log_group_name = data.aws_route53_zone.primary.zone_id
  # "name" deliberately omitted - required, no value at all. This is the
  # independent hard failure that must veto the cascade below.
}

resource "aws_cloudwatch_log_subscription_filter" "dependent" {
  log_group_name = aws_cloudwatch_log_stream.parent.log_group_name
  name           = "dependent-filter"
}
