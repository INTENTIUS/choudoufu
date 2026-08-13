# Fixture for #45's route regression guard: aws_route's own shape, the same
# one internal/live/identity/testdata/schema-fallback-route uses to pin #39
# from the resolver side. Here it pins the same refusal from lint's
# admission check, with aws_route's table row bypassed in the test so the
# schema fallback is what actually gets exercised - aws_route is ordinarily
# in admittedTypesV0 and would never reach SynthesizeTypeIdentity otherwise.
# route_table_id is the only required identity attribute, and
# destination_cidr_block is one of three optional-for-import alternatives:
# a single required attribute that is not the whole identity, which #39
# taught the fallback to refuse rather than mis-synthesize.

resource "aws_route" "r" {
  route_table_id         = "rtb-0123456789abcdef0"
  destination_cidr_block = "10.0.0.0/16"
}
