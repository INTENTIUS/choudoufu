# Limits fixture: RuleIgnoreTags (GitHub issue #2290).
#
# "tofu-" is the start of every marker key, so this provider hides the
# markers from every read it makes. The bucket is ordinary; the refusal is
# about the provider block.

provider "aws" {
  region = "us-east-1"

  ignore_tags {
    key_prefixes = ["tofu-"]
  }
}

resource "aws_s3_bucket" "q" {
  bucket = "limits-ignore-tags"
}
