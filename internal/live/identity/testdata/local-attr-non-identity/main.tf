# The local-values fix must stay bound by the same identity-attribute
# restriction a direct reference already has: reading a NON-identity
# attribute of a parent through a local still refuses, rather than
# resolving to a value nothing about the parent's identity backs.
# aws_route53_zone's identity is [id, zone_id]; "name" (the domain name)
# is not in that list.

resource "aws_route53_zone" "public" {
  name = "example.com"
}

locals {
  zone_domain = aws_route53_zone.public.name
}

resource "aws_route53_record" "www" {
  zone_id = local.zone_domain
  name    = "www"
  type    = "A"
}
