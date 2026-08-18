# Component.OmitIfAbsent's vpc_region segment (#286). The provider (aws
# 6.59.0) documents two import forms in the same Import section:
#
#	Z123456ABCDEFG:vpc-12345678
#	Z123456ABCDEFG:vpc-12345678:us-east-2
#
# ("The VPC is in the same region where you have configured the Terraform
# AWS Provider" vs. "The VPC is _not_ in the same region...") - so a
# same-region association has no trailing colon-plus-region segment at all,
# not an empty one. A cross-region association would otherwise collide with
# a same-region association of the identical VPC id in the identical zone.
resource "aws_route53_zone_association" "same_region" {
  zone_id = "Z123456ABCDEFG"
  vpc_id  = "vpc-12345678"
}

resource "aws_route53_zone_association" "cross_region" {
  zone_id    = "Z123456ABCDEFG"
  vpc_id     = "vpc-12345678"
  vpc_region = "us-east-2"
}
