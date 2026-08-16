# A two-link chain into an identity argument: the phase must demand both,
# classify both eligible, and order the dependency first. The unused data
# source must not be demanded at all - demand is driven by what identity
# resolution asks for, not by what the configuration declares.
data "aws_route53_zone" "primary" {
  name = "example.com."
}

data "aws_route53_zone" "sub" {
  name = "${data.aws_route53_zone.primary.name}sub."
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.aws_route53_zone.sub.zone_id}"
}

data "aws_region" "unused" {}
