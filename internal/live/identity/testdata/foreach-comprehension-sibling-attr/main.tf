# GitHub issue #220, confirmed site: simpleinfra's
# terraform/fastly-tls-subscription/main.tf. The for_each's key clause reads
# fastly_tls_subscription.subscription.domains, which is set verbatim from
# var.domains in that resource's own block; only the value clause touches
# managed_dns_challenges, a genuinely apply-time attribute for_each does not
# need. fastly_tls_subscription is stood in for by test_domain_source, a
# schema-only fake type (this fixture supplies Context.Schemas, not a real
# provider), so the test isolates the for_each-collection question from
# fastly_tls_subscription's own unrelated admission gap.

resource "test_domain_source" "subscription" {
  domains = ["a.example.com", "b.example.com"]
}

resource "aws_route53_record" "tls_validation" {
  for_each = {
    for domain in test_domain_source.subscription.domains :
    domain => element([
      for obj in test_domain_source.subscription.managed_dns_challenges :
      obj if obj.record_name == "_acme-challenge.${domain}"
    ], 0)
  }

  zone_id = "Z1234567890"
  name    = each.value.record_name
  type    = each.value.record_type
  ttl     = 60
  records = [each.value.record_value]
}
