# Fixture for the residue roster's registry-laggard cohort (issue #49).
# aws_ec2_client_vpn_authorization_rule is outside the v0 table and, per
# live/mapping.json and live/registry.json, maps to
# AWS::EC2::ClientVpnAuthorizationRule, whose Registry entry ships no
# working handler at all. The rejected3 batch (2026-08-16) verified this
# type's identity cleanly (server-assigned via id) but found the provider's
# own Import section needs client_vpn_endpoint_id and target_network_cidr -
# both real, Required arguments the registry's own composite-free
# primaryIdentifier claim never named - so it stayed rejected rather than
# admitted with an incomplete key (tools/row-gen/rejected.json). This
# fixture previously used aws_codebuild_source_credential before the same
# batch admitted it by correcting row-gen's proposal against the provider's
# own documented import behaviour, and moved to a still-unadmitted type so
# this test keeps exercising the registry-laggard cohort rather than
# tripping over the admission table's own growth.

resource "aws_ec2_client_vpn_authorization_rule" "example" {
  client_vpn_endpoint_id = "cvpn-endpoint-0ac3a1abbccddd666"
  target_network_cidr    = "10.1.0.0/24"
  authorize_all_groups   = true
}
