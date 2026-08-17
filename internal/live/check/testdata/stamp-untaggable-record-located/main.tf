terraform {
  live {
    estate = "stamp-untaggable-record-located"

    # The onboarded form. GitHub issue #270: the marker answers "may I
    # delete this" and the identity answers "which object is this", and
    # declaring a record_store is what supplies the second for a type that
    # can never carry the first.
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "aws_cloudfront_cache_policy" "example" {
  name    = "example-policy"
  min_ttl = 1
}
