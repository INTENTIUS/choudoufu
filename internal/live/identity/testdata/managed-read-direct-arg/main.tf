# The direct leg of the provenance discriminator: an identity argument that
# names a covered managed resource's attribute itself, with no for_each and
# no each.value in sight. The certificate's arn is minted at create, so the
# log group's name is not knowable until the certificate has been applied.
resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_cloudwatch_log_group" "app" {
  name = aws_acm_certificate.cert.arn
}
