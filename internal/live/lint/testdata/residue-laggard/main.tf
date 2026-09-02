# Fixture for the residue roster's registry-laggard cohort (issue #49).
# aws_config_delivery_channel is outside the v0 table and, per
# live/mapping.json and live/registry.json, maps to a CloudFormation type
# whose Registry entry ships no working handler at all. Its survey row reads
# "identity attrs (name) are settable but not required arguments, so
# client-naming is unprovable from the schema; untaggable, no native list
# resource and no Cloud Control list handler", which is ordinary admission
# debt: a config signal naming every block explicitly can still admit it.
#
# Two types held this fixture's place before it. aws_codebuild_source_credential
# left when the rejected3 batch (2026-08-16) admitted it. aws_ec2_client_vpn_authorization_rule
# left when RuleMarkerlessType landed: it is on
# internal/live/identity.MarkerlessTypes, and the markerless-type refusal
# deliberately carries no residue cohort sentence, so a fixture using it
# stopped exercising this test's subject entirely. The replacement has to be
# unadmitted, in the registry-laggard cohort, and off the markerless roster -
# all three, or this test measures something other than what it names.

resource "aws_config_delivery_channel" "example" {
  name           = "example"
  s3_bucket_name = "example-config-bucket"
}
