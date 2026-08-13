# Type choice, recorded per the issue's instructions.
#
# aws_s3_bucket: taggable, client-named (its own identity is the "bucket"
# argument - internal/live/identity/table.go), and needs no marker
# discovery at all to resolve, which is deliberate here: the discovery
# pass's own provider selection (internal/command/live_plan.go's
# statelessDiscoveryProvider) refuses a run whose *needs-discovery*
# instances span more than one provider configuration ("Marker discovery
# v0 goes through one provider configuration per run"). Two client-named
# resources under two aliases isolates the question this fixture exists to
# ask - does a plan over resources from two aliased provider
# configurations work at all - from that unrelated, already-documented v0
# restriction on simultaneous multi-region marker discovery.

resource "aws_s3_bucket" "east" {
  bucket = "tofu-alias-e2e-east"

  tags = {
    tofu-estate  = "alias-e2e"
    tofu-address = "aws_s3_bucket.east"
  }
}

resource "aws_s3_bucket" "west" {
  provider = aws.west
  bucket   = "tofu-alias-e2e-west"

  tags = {
    tofu-estate  = "alias-e2e"
    tofu-address = "aws_s3_bucket.west"
  }
}
