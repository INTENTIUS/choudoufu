# Two data sources that read each other: no order reads a cycle, so both
# classify ineligible rather than looping or crashing.
data "aws_route53_zone" "a" {
  name = data.aws_route53_zone.b.name
}

data "aws_route53_zone" "b" {
  name = data.aws_route53_zone.a.name
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.aws_route53_zone.a.zone_id}"
}
