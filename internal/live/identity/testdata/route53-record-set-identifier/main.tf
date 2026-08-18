# Component.OmitIfAbsent's set_identifier segment (#286). The provider (aws
# 6.59.0) documents two import forms in the same Import section:
#
#	Z4KAPRWWNC7JR_dev.example.com_NS
#	Z4KAPRWWNC7JR_dev.example.com_NS_dev
#
# "If the record also contains a set identifier, append it" - so a record
# with no set_identifier has no trailing underscore-plus-value segment at
# all, not an empty one. Weighted, latency and failover routing records
# share a zone, a name and a type, and set_identifier is the only thing
# Route 53 lets an owner use to tell two such records apart - so two
# declarations differing only in set_identifier is the exact collision this
# row exists to prevent.
resource "aws_route53_record" "unweighted" {
  zone_id = "Z4KAPRWWNC7JR"
  name    = "dev.example.com"
  type    = "NS"
  ttl     = 300
  records = ["ns1.example.com"]
}

resource "aws_route53_record" "weighted" {
  zone_id = "Z4KAPRWWNC7JR"
  name    = "dev.example.com"
  type    = "NS"
  ttl     = 300
  records = ["ns1.example.com"]

  set_identifier = "dev"
  weighted_routing_policy {
    weight = 10
  }
}
