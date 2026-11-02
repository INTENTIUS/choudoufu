# Limits fixture: RuleAWSTagCount (GitHub issue #2131).
#
# 46 tags on the bucket and 3 from default_tags make 49: stock creates this
# bucket, and AWS's cap is 50. The two markers this run adds (tofu-estate,
# tofu-address) make 51, so the create would fail only because of them.

provider "aws" {
  region = "us-east-1"

  default_tags {
    tags = {
      CostCenter  = "platform"
      Environment = "limits"
      Team        = "infra"
    }
  }
}

resource "aws_s3_bucket" "crowded" {
  bucket = "limits-aws-tag-count"
  tags   = { for i in range(46) : "tag-${i}" => "x" }
}
