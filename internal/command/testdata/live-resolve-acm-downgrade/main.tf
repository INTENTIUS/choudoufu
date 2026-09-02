# The ACM shape with the trade that makes a second pass a net loss, which is
# simpleinfra's shared/modules/acm-certificate reduced to two blocks.
#
# The for_each is what the second pass settles. The log group is what it
# breaks: its name is a formula over the certificate's arn, which a first pass
# resolves symbolically as PARENT_DERIVED and marker discovery can render once
# the certificate is found. Give resolution the certificate's PLANNED value
# and the reference becomes evaluable instead of symbolic, evaluates to an
# unknown, and the instance drops to NEEDS_DISCOVERY.
#
# Counting identity's own error diagnostics calls that a win: one refusal
# cleared, none raised. In the real estate the demoted type is untaggable, so
# the demotion becomes a hard refusal in internal/live/stamp - downstream of
# where the two passes are compared, and invisible to the comparison.
#
# The name is wrapped in a template rather than written as the bare
# `aws_acm_certificate.cert.arn` reference issue #284's managedCovered fix
# now rescues: [resolver.resolveExpr]'s retry only fires on
# hcl.AbsTraversalForExpr(expr), which a template with literal text around
# the reference is not. This fixture is what is left of the downgrade once
# that fix lands, and it is what keeps this test proving the ratchet rather
# than a scenario the fix has already made unreachable.
resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_cloudwatch_log_group" "app" {
  name = "app-${aws_acm_certificate.cert.arn}"
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
