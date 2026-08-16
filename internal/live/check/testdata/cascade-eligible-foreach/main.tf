# Same cascade shape as ../cascade-eligible/main.tf, for_each'd: pins that
# the recovered parent address carries the instance key
# (aws_cloudwatch_log_group.per_zone["a"]) rather than the bare resource
# address, so two different keys never collide in eligibleAddrs.
locals {
  zones = toset(["a", "b"])
}

data "aws_route53_zone" "primary" {
  for_each = local.zones
  name     = "${each.key}.example.com."
}

resource "aws_cloudwatch_log_group" "per_zone" {
  for_each = local.zones
  name     = "/zones/${data.aws_route53_zone.primary[each.key].zone_id}"
}

resource "aws_cloudwatch_log_stream" "per_zone" {
  for_each       = local.zones
  log_group_name = aws_cloudwatch_log_group.per_zone[each.key].name
  name           = "stream"
}
