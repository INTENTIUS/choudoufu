# Same cascade shape as ../cascade-eligible/main.tf, for_each'd: pins that
# the recovered parent address carries the instance key
# (aws_s3_bucket.per_zone["a"]) rather than the bare resource address, so
# two different keys never collide in eligibleAddrs.
#
# aws_s3_bucket_policy, single-real-component - see
# ../cascade-eligible/main.tf's comment on why this fixture no longer uses
# aws_cloudwatch_log_stream.
locals {
  zones = toset(["a", "b"])
}

data "aws_route53_zone" "primary" {
  for_each = local.zones
  name     = "${each.key}.example.com."
}

resource "aws_s3_bucket" "per_zone" {
  for_each = local.zones
  bucket   = "zone-${data.aws_route53_zone.primary[each.key].zone_id}"
}

resource "aws_s3_bucket_policy" "per_zone" {
  for_each = local.zones
  bucket   = aws_s3_bucket.per_zone[each.key].bucket
}
