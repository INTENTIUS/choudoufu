# Coverage: marker path with a domain-name content match
# (aws_acm_certificate — ACM mints the certificate ARN at create time, and
# the domain name is not an identity: ACM permits several certificates for
# the same domain, the same shape as Route 53's two-zones-one-name, and the
# adoption table's one-to-one rule refuses that ambiguity rather than
# resolving it). Third slice of the survey's marker cohort (#20).

resource "aws_acm_certificate" "app" {
  domain_name       = "app.stateless-e2e.example.com"
  validation_method = "DNS"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_acm_certificate.app"
  }
}
