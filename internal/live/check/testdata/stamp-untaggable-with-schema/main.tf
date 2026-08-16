terraform {
  live {
    estate = "stamp-untaggable-with-schema"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "aws_cloudfront_cache_policy" "example" {
  name    = "example-policy"
  min_ttl = 1
}
