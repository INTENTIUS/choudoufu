# A resource whose identity-bearing computed attribute the provider derives
# from arguments the configuration states. The shape of
# aws_acm_certificate.domain_validation_options, with the provider-specific
# names removed: `derived` is computed, and a provider fills it from `names`.
resource "stub_cert" "cert" {
  names = ["example.com", "www.example.com"]
}

# Not planned: it repeats, and one planned value cannot stand for a key set
# that is itself in question.
resource "stub_cert" "repeated" {
  count = 2
  names = ["a.example.com"]
}
