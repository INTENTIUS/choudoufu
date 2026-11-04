resource "aws_route53_zone" "z" {
  name          = "${var.sfx}-fd.example.internal"
  force_destroy = true
  vpc { vpc_id = aws_vpc.v.id }
}
resource "aws_vpc" "v" {
  cidr_block           = "10.235.0.0/16"
  enable_dns_support   = true
  enable_dns_hostnames = true
}
