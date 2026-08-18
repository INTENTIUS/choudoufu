terraform {
  live {
    estate = "stamp-untaggable-with-schema"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "aws_cloudfront_origin_access_control" "example" {
  name    = "example-policy"
  min_ttl = 1
}
