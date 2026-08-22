# NO live block, and its absence is load-bearing.
#
# This fixture is the "not onboarded" side of GitHub issue #270's split: a
# type with nowhere to write a marker and nothing to record which object it
# is, which is still refused and must stay refused. The onboarded side - the
# same shape under a record store, admitted as RECORD_LOCATED - is
# stamp-untaggable-record-located next door.
#
# It carried a live block with no record_store until choudoufu #364, which
# is when that stopped meaning "no store": every live block now gets an
# implied local one (internal/configs.impliedRecordStore), so a live block
# here would put this fixture on the OTHER side of the split and take
# TestStampGate_GenuinelyUntaggableTypeStillRefuses with it. Adding one back
# does exactly that. What is left on this side is a configuration nobody has
# adopted - which is what `choudoufu live-check` reads, and the state
# ClassifyOnboarding exists to classify.
#
# It declared a record_store before #270 landed, and that did nothing:
# before that issue the store had no bearing on a markerless type at all.

# aws_cloudfront_origin_access_control is issue #272's permanent negative
# case. It sits beside the two CloudFront policy types in the same service,
# is untaggable in the same way, is listed by Cloud Control in the same way,
# and appears in the same estates - and it stays refused, because neither of
# the two texts the unique-name rule reads makes the claim. Its
# CreateOriginAccessControl error says "An origin access control with the
# specified parameters already exists" rather than naming the name, its
# CloudFormation schema says "A name to identify the origin access control"
# with no uniqueness claim, and the provider's own argument reference says "A
# name that identifies the Origin Access Control". The differently-worded
# error suggests the dedup key is the whole configuration tuple rather than
# the name alone, which would make a name match bind the wrong object.
#
# This fixture used to declare aws_cloudfront_cache_policy, which #272
# admitted. Swapping the type is what keeps the test about a genuinely
# unfindable resource rather than about a type that has since acquired a way
# to be found.
resource "aws_cloudfront_origin_access_control" "example" {
  name                              = "example-oac"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}
