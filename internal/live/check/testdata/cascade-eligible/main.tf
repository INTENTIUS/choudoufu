# #184's cascade shape: the bucket's own identity reads a data source
# (offline-ineligible, so identity refuses it - the corpus's dominant idiom,
# same as data-read-eligible/main.tf), and the bucket policy's identity, in
# turn, reads the bucket's identity attribute. Offline, that second
# reference cascades into identity's own "Unresolvable identity" - a
# diagnostic identity/resolve.go raises with no reference back to the data
# read that actually caused it. Both sites must land as one eligible-read
# finding, not one eligible plus one hard "Unresolvable identity" refusal.
#
# aws_s3_bucket_policy is deliberately single-real-component (its identity
# is "bucket" alone - table_generated.go), not the two-component
# aws_cloudwatch_log_stream this fixture used before #221: a dependent with
# a second real component is exactly what #221's conservative fix refuses
# to reclassify, on purpose, regardless of whether that second component
# would in fact have been fine. See testdata/cascade-multicomponent-blocked
# for that case.
data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_s3_bucket" "per_zone" {
  bucket = "zone-${data.aws_route53_zone.primary.zone_id}"
}

resource "aws_s3_bucket_policy" "per_zone" {
  bucket = aws_s3_bucket.per_zone.bucket
}
