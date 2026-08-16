# GitHub issue #220, confirmed site: DataCite's mastino/global/dns/main.tf.
# aws_route53_zone.production's identity is zone_id, so "name" is correctly
# excluded from its IdentityAttrs - but the mx-datacite record does not read
# name as an identity reference at all. It reads a plain string the zone's
# own block wrote literally ("datacite.org"), the same way it could have
# read a local or a variable.

resource "aws_route53_zone" "production" {
  name = "datacite.org"
}

resource "aws_route53_record" "mx-datacite" {
  zone_id = aws_route53_zone.production.zone_id
  name    = aws_route53_zone.production.name
  type    = "MX"
  ttl     = "300"
  records = ["1 aspmx.l.google.com"]
}
