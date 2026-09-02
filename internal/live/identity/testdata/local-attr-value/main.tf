# #178's local-values fix: an identity argument reads a local whose own
# definition is a managed resource's attribute, with nothing else in the
# way. aws_route53_zone is server-assigned, so the zone's own resolution is
# NEEDS_DISCOVERY and the record's zone_id has to come back as a Formula
# part naming the zone, not as a flat string.

resource "aws_route53_zone" "public" {
  name = "example.com"
}

locals {
  zone_id = aws_route53_zone.public.zone_id
}

resource "aws_route53_record" "www" {
  zone_id = local.zone_id
  name    = "www"
  type    = "A"
}
