// Every optional argument set, so the decoder's full field set gets
// exercised: tag_key/tag_value distinct from the estate marker, a scope
// block on the delete quadrant, and a threshold.
terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged     = "untag"
      declared_untagged   = "converge"
      undeclared_tagged   = "keep"
      undeclared_untagged = "delete"

      tag_key   = "preserve"
      tag_value = "yes"

      threshold = 25

      scope {
        services = ["ec2", "s3"]
        types    = ["aws_instance"]
        regions  = ["us-east-1", "us-west-2"]
      }
    }
  }
}
