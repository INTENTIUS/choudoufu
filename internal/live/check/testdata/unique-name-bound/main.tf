# The four types GitHub issue #272 admitted: untaggable and server-assigned,
# which is the markerless veto's own predicate word for word, and admitted
# anyway because the provider's argument reference and the CloudFormation
# registry schema independently document the name as unique within the
# account and region.
#
# It is here for TestIdentityGolden. Every other instrument in this
# repository counts refusals, and a rendered identity can be WRONG without
# anything refusing - so the four rows are pinned by the value they render,
# not only by the fact that they resolve. What they must render is a
# NEEDS_DISCOVERY resolution with an EMPTY import ID: the name is what finds
# the object in a listing, and the import ID is the opaque value CloudFront
# and Route 53 mint, which no configuration states. The provider's own
# documented import examples say exactly that - the cache policy page shows
# id = "658327ea-f89d-4fab-a63d-7e88639e58f6" for a resource whose documented
# name is "example-policy" - and a line here that ever rendered a name as an
# import ID would be that confusion shipping.

terraform {
  live {
    estate = "unique-name-bound"
  }
}

resource "aws_cloudfront_cache_policy" "example" {
  name    = "example-policy"
  min_ttl = 1
}

resource "aws_cloudfront_origin_request_policy" "example" {
  name = "example-origin-request-policy"

  cookies_config {
    cookie_behavior = "none"
  }
  headers_config {
    header_behavior = "none"
  }
  query_strings_config {
    query_string_behavior = "none"
  }
}

resource "aws_cloudfront_response_headers_policy" "example" {
  name = "example-response-headers-policy"
}

resource "aws_route53_cidr_collection" "example" {
  name = "example-cidr-collection"
}
