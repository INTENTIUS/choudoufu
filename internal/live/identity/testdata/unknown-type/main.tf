# A resource type with no entry in the v0 identity table. Resolution must
# refuse it by name rather than assume anything about its identity.
resource "aws_customer_gateway" "app" {
  bgp_asn    = 65000
  ip_address = "172.0.0.1"
  type       = "ipsec.1"
}
