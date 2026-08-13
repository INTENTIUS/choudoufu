# Fixture for RuleUnadmittedType. aws_customer_gateway is a real,
# non-logical, server-assigned type simply not wired yet, in no residue
# cohort (TestRefusalSilentForTypeInNoCohort below relies on that).

resource "aws_customer_gateway" "web" {
  bgp_asn    = 65000
  ip_address = "172.0.0.1"
  type       = "ipsec.1"
}
