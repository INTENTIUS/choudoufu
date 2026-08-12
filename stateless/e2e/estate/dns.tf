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
