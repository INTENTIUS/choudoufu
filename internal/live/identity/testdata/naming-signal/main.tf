# Every way a configuration can answer "do you name this object", in one
# module. Read by the config-side naming signal (signal.go), which is what
# separates the Optional+Computed identity attributes a configuration
# supplies from the ones the cloud does.
#
# Nothing here is meant to resolve: three of these blocks are deliberately
# missing an identity argument, and Resolve says so. The signal is what can
# still be read off them.

variable "unset_name" {
  type    = string
  default = null
}

# Names itself outright.
resource "aws_s3_bucket" "named" {
  bucket = "tofu-signal-named"

  tags = {
    env = "test"
  }
}

# Does not: bucket_prefix leaves the rest of the name to S3, and it is the
# reason the provider marks bucket Optional+Computed in the first place.
resource "aws_s3_bucket" "prefixed" {
  bucket_prefix = "tofu-signal-"
}

# Written, and null. This is the idiom that makes presence alone a lie: the
# argument is there in the source and the apply will still let S3 name the
# bucket.
resource "aws_s3_bucket" "nulled" {
  bucket = var.unset_name
}

# Nothing in a VPC block names the VPC. aws_vpc's id attribute is
# Optional+Computed in the legacy-SDK schema exactly as aws_s3_bucket's
# bucket is, which is why the schemas cannot tell these two apart and this
# file can.
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

# Two identity arguments, one supplied. Half an identity is not an identity,
# and the type verdict has to say so rather than round up.
resource "aws_lb_target_group_attachment" "half" {
  target_group_arn = "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/tg/1"
}

# Per instance, not per block: index 0 names itself and index 1 does not,
# out of one body.
resource "aws_cloudwatch_log_group" "split" {
  count = 2
  name  = count.index == 0 ? "/signal/zero" : null
}
