data "aws_caller_identity" "me" {}
resource "aws_iam_role" "a" {
  name               = "${var.sfx}-a"
  assume_role_policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = "sts:AssumeRole", Principal = { Service = "ec2.amazonaws.com" } }] })
}
resource "aws_iam_role" "b" {
  name               = "${var.sfx}-b"
  assume_role_policy = aws_iam_role.a.assume_role_policy
}
resource "aws_s3_bucket" "b" {
  bucket        = "${var.sfx}-policy-${data.aws_caller_identity.me.account_id}"
  force_destroy = true
}
locals {
  principals = [aws_iam_role.a.arn, aws_iam_role.b.arn]
  actions    = ["s3:GetObject", "s3:PutObject", "s3:ListBucket"]
}
resource "aws_s3_bucket_policy" "p" {
  bucket = aws_s3_bucket.b.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "x"
      Effect    = "Allow"
      Principal = { AWS = local.principals }
      Action    = local.actions
      Resource  = [aws_s3_bucket.b.arn, "${aws_s3_bucket.b.arn}/*"]
    }]
  })
}
resource "aws_iam_policy" "ip" {
  name = "${var.sfx}-ip"
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = local.actions, Resource = [aws_s3_bucket.b.arn, "${aws_s3_bucket.b.arn}/*"] }]
  })
}
