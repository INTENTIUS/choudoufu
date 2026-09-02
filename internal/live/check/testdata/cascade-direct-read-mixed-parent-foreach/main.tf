# The parent-side counterpart of ../cascade-mixed-per-foreach-key, and the
# reason the parent gate is keyed by instance address rather than by
# resource. Both keys of aws_cloudwatch_log_stream.parent read the same
# eligible data source directly, so both enter eligibleAddrs through the
# direct route; only key "b"'s "name" is null, so only parent["b"] is also
# hard-failing.
#
# A parent gate that collapsed the two keys - checking the resource rather
# than the instance - would hold back dependent["a"] as well, refusing a
# reference that is genuinely fine. dependent["a"] must reclassify;
# dependent["b"] must not.
locals {
  names = {
    a = "stream-a"
    b = null
  }
}

data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_cloudwatch_log_stream" "parent" {
  for_each       = local.names
  log_group_name = data.aws_route53_zone.primary.zone_id
  name           = each.value
}

resource "aws_cloudwatch_log_subscription_filter" "dependent" {
  for_each       = local.names
  log_group_name = aws_cloudwatch_log_stream.parent[each.key].log_group_name
  name           = "filter-${each.key}"
}
