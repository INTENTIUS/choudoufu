resource "aws_acm_certificate" "this" {
  count = 1

  domain_name       = "wild.example.com"
  validation_method = "DNS"
}

resource "aws_acm_certificate_validation" "this" {
  count = 1

  certificate_arn = aws_acm_certificate.this[0].arn
}

output "acm_certificate_arn" {
  value = try(aws_acm_certificate_validation.this[0].certificate_arn, aws_acm_certificate.this[0].arn, "")
}
