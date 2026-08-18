# Fixture for TestScanTypeContentMatch (contentmatch_e2e_test.go): a real
# aws_cloudfront_cache_policy declaration, which identity.ContentMatchTypes
# already carries a real binding for (Argument "name", CFN type
# AWS::CloudFront::CachePolicy, PropertyPath CachePolicyConfig.Name) - so
# this exercises the actual generated table rather than a synthetic one.

resource "aws_cloudfront_cache_policy" "x" {
  name    = "my-policy"
  min_ttl = 1
}
