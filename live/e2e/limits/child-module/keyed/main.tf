# The for_each-expanded call's target. Its own contents are inside the
# stateless subset, so the only thing the fixture proves is that the call
# itself - "keyed" in ../main.tf - is still refused: its for_each reads
# aws_s3_bucket.data.bucket, which is not knowable from configuration alone,
# so RuleChildModule refuses it as non-static (see "keyed-static" for the
# admitted case, where the keys are a literal set of strings instead).

resource "aws_vpc" "main" {
  cidr_block = "10.45.0.0/16"
}
