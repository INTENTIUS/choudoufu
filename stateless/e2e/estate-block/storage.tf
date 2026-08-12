# Coverage: client-named path (aws_s3_bucket — identity is the bucket name
# in config, nothing to recover). No aws_s3_bucket_policy here: this fixture
# trims the named-singleton-child and attachment-composite rows the main
# estate already covers (README.md, "Subset chosen").

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-e2e-block-data"
}
