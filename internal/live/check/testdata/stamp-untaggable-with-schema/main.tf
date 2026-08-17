terraform {
  live {
    estate = "stamp-untaggable-with-schema"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

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
