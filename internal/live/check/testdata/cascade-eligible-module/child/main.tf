# Same cascade shape as ../../cascade-eligible/main.tf, but inside a child
# module: identity.StaticIdentifier.String() prefixes NeededBy with
# "module.child:" while the resource-instance address it wraps already
# carries "module.child." from addrs.AbsResourceInstance.String() - the two
# module prefixes are spelled differently (":" vs ".") and the second is
# redundant. This fixture pins that the address recovered from NeededBy
# still matches the cascade diagnostic's own parent text exactly.
#
# aws_s3_bucket_policy, single-real-component - see
# ../../cascade-eligible/main.tf's comment on why this fixture no longer
# uses aws_cloudwatch_log_stream.
data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_s3_bucket" "per_zone" {
  bucket = "zone-${data.aws_route53_zone.primary.zone_id}"
}

resource "aws_s3_bucket_policy" "per_zone" {
  bucket = aws_s3_bucket.per_zone.bucket
}
