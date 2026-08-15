# A local whose value is an object constructor, one field a resource
# reference and one a plain literal, each selected by a different resource.
# Before #178's local-values fix, BOTH records failed: evaluating
# local.zones as a whole to answer the "static" one raised the same
# diagnostic as the "dynamic" one, because GetLocalValue evaluates the
# whole object regardless of which field is asked for. This fixture pins
# that the literal sibling is not collateral damage.

resource "aws_route53_zone" "public" {
  name = "example.com"
}

locals {
  zones = {
    primary = aws_route53_zone.public.zone_id
    literal = "Z000STATIC"
  }
}

resource "aws_route53_record" "dynamic" {
  zone_id = local.zones.primary
  name    = "dyn"
  type    = "A"
}

resource "aws_route53_record" "static" {
  zone_id = local.zones.literal
  name    = "stat"
  type    = "A"
}
