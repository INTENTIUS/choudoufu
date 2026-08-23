# A resource whose identity-bearing computed attribute the provider derives
# from arguments the configuration states. The shape of
# aws_acm_certificate.domain_validation_options, with the provider-specific
# names removed: `derived` is computed, and a provider fills it from `names`.
resource "stub_cert" "cert" {
  names = ["example.com", "www.example.com"]
}

# Planned once per instance, not skipped: count is a literal, so the key
# set - {0, 1} - is not in question at all, the exact distinction a for_each
# whose source is computed does not have.
resource "stub_cert" "repeated" {
  count = 2
  names = ["a.example.com"]
}

# Not planned: count itself reads a managed resource's own computed
# attribute, so the key set IS in question - the identical hazard a for_each
# built from stub_cert.cert.derived would decline for, now reachable through
# count instead of for_each.
resource "stub_cert" "dynamic_count" {
  count = length(stub_cert.cert.derived)
  names = ["b.example.com"]
}
