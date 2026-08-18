# The exact bare-reference shape issue #284's managedCovered fix rescues:
# simpleinfra's shared/modules/acm-certificate module reduced to its
# for_each and its one DIRECT reference to the certificate's arn, on a type
# ([aws_cloudwatch_log_group], unlike aws_acm_certificate_validation) that is
# in the ratified admission table. See
# TestStatelessResolveAcceptsTheSecondPassOnceTheDirectFormulaSurvives.
resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_cloudwatch_log_group" "app" {
  name = aws_acm_certificate.cert.arn
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.cert.domain_validation_options : dvo.domain_name => {
      name = dvo.resource_record_name
      type = dvo.resource_record_type
    }
  }

  zone_id = "Z0423220"
  name    = each.value.name
  type    = each.value.type
  records = ["ignored"]
  ttl     = 60
}
