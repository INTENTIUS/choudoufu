# each.value with no for_each set on the block at all: not this block's own
# repetition value, so the self-repetition relaxation must not apply - this
# stays refused exactly as it always has, matching stock OpenTofu's own
# "each.value cannot be used in this context" error for the same
# construct.
data "aws_route53_zone" "no_for_each" {
  name = each.value
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.aws_route53_zone.no_for_each.zone_id}"
}
