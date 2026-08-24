# terraform-aws-modules/terraform-aws-acm's own shape, reduced:
# aws_route53_record.validation takes its name and type out of
# aws_acm_certificate.this's domain_validation_options, which the provider
# does not fill in until the certificate has been applied - so the identity
# does not fold from configuration this run, and the honest classification is
# DiscoverySiblingApply.
#
# aws_route53_record has a ratified table row and NO tags map, so a
# sibling-apply NEEDS_DISCOVERY answer is a dead end for it: the marker sweep
# that answer promises has nothing to sweep, and internal/command/live_plan
# escalates the unstamped instance to "Unmarked apply of a marker-only
# resource". The record rung is the only identity source left that is not a
# guess. See TestRecordFallbackClassifiesSiblingApplyUntaggable.
#
# aws_s3_bucket.logs is the control: the identical sibling-apply dependency
# on a TAGGABLE type, which must keep the NEEDS_DISCOVERY answer it has
# always had, because its marker really can be swept.
terraform {
  live {
    estate = "record-fallback-sibling-apply"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "aws_acm_certificate" "this" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_route53_record" "validation" {
  zone_id = "Z0000000000000000001"
  name    = tolist(aws_acm_certificate.this.domain_validation_options)[0].resource_record_name
  type    = tolist(aws_acm_certificate.this.domain_validation_options)[0].resource_record_type
  ttl     = 60
  records = [tolist(aws_acm_certificate.this.domain_validation_options)[0].resource_record_value]
}

resource "aws_s3_bucket" "logs" {
  bucket = tolist(aws_acm_certificate.this.domain_validation_options)[0].resource_record_name
}
