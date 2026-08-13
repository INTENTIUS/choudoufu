# Fixture for the residue roster's emulator-blocked cohort (issue #49).
# aws_cloudfront_distribution is outside the v0 table today: floci serves no
# usable CloudFront distribution lifecycle (lex00/floci#29), so issue #26
# keeps it out of a wiring slice. (This fixture's example used to be
# aws_db_instance, which moved to Admitted: true in live/residue.go's
# EmulatorBlocked roster once the RDS batch — issue #65's ratification
# campaign — admitted it on paper despite the same emulator gap family;
# aws_instance was considered as the replacement but is reserved elsewhere
# as the "surveyed but unadmitted, in no cohort" example — see
# live/residue.go.)

resource "aws_cloudfront_distribution" "cdn" {
  enabled = true

  origin {
    domain_name = "example.s3.amazonaws.com"
    origin_id   = "example"
  }

  default_cache_behavior {
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    target_origin_id       = "example"
    viewer_protocol_policy = "redirect-to-https"
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }
}
