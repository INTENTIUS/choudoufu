# Coverage: marker path with a non-"id" identity attribute
# (aws_route53_zone — Route 53 mints the hosted zone ID, and the provider's
# identity schema for the type names zone_id rather than id, so discovery
# reads the identity from that attribute). Same slice as keys.tf (#20).
#
# force_destroy is deliberately not set, for the same reason keys.tf leaves
# deletion_window_in_days alone: Route 53 never returns it, so under markers
# it would be a permanent in-place diff. The zone holds nothing but its own
# SOA and NS records, which a destroy removes with it, so the flag buys
# nothing here anyway.

resource "aws_route53_zone" "main" {
  name = "stateless-e2e.example.com"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_route53_zone.main"
  }
}

# Coverage: composite through a marker-discovered parent
# (aws_route53_record — the import ID is ZONEID_NAME_TYPE; name and type are
# client-named but the Z-ID is the zone's server-assigned identity, flag F5
# in stateless/SURVEY.md, resolved by wiring the zone above). No tags
# argument on this resource type — untaggable by type. No set_identifier:
# the identity table's components deliberately build only the plain-record
# grammar. #19's second slice.
resource "aws_route53_record" "app" {
  zone_id = aws_route53_zone.main.zone_id
  name    = "app.stateless-e2e.example.com"
  type    = "A"
  ttl     = 300
  records = ["10.42.0.10"]
}
