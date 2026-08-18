# Fixture for TestStaticArgumentValue (contentmatch_test.go): one instance
# per shape staticArgumentValue has to tell apart. No provider block and no
# real resource type - loadConfig only ever reads the resource's own Config
# body, never a schema, so the type names here are placeholders.

locals {
  policy_name = "from-a-local"
}

resource "aws_cloudfront_cache_policy" "literal" {
  name = "literal-name"
}

resource "aws_cloudfront_cache_policy" "from_local" {
  name = local.policy_name
}

resource "aws_cloudfront_cache_policy" "no_name" {
  min_ttl = 1
}

resource "aws_cloudfront_cache_policy" "empty" {
  name = ""
}

resource "aws_s3_bucket" "other" {
  bucket = "irrelevant"
}

resource "aws_cloudfront_cache_policy" "dynamic" {
  name = aws_s3_bucket.other.arn
}
