data "aws_caller_identity" "me" {}
resource "aws_kms_key" "k" {
  description             = "${var.sfx} repro"
  deletion_window_in_days = 7
}
resource "aws_kms_key_policy" "p" {
  key_id = aws_kms_key.k.key_id
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Sid = "root", Effect = "Allow", Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.me.account_id}:root" }, Action = "kms:*", Resource = "*" }]
  })
}
