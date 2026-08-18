# Two sources in one argument, and the managed one is NOT the unknown: the
# certificate's domain_name is wholly known in the results this run holds,
# while the data source beside it is not.
#
# It is the fixture for the rule that leg A of the discriminator asks whether
# the COVERED value is unknown, not merely whether the expression mentions a
# covered resource. Attributing this to the certificate would tell an operator
# to apply a certificate that is already applied, about an unknown that came
# from somewhere else entirely.
data "aws_region" "current" {}

resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_cloudwatch_log_group" "app" {
  name = "${aws_acm_certificate.cert.domain_name}-${data.aws_region.current.name}"
}
