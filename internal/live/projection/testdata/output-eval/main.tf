# A resource this test's state carries a full instance for, so referencing
# its attribute from a root output resolves to a wholly-known value.
resource "stub_cert" "cert" {
  names = ["example.com"]
}

# A resource this test's state carries NO instance for - the "about to be
# created" case. Any output that reads it has to come back unknown, not a
# concrete value the eval graph invented.
resource "stub_cert" "future" {
  names = ["future.example.com"]
}

# A plain resource-attribute reference.
output "cert_id" {
  value = stub_cert.cert.id
}

# An expression built from a resource attribute, not a bare reference.
output "cert_label" {
  value = "cert-${stub_cert.cert.id}"
}

# A sensitive output.
output "cert_secret" {
  value     = stub_cert.cert.id
  sensitive = true
}

# Reads the not-yet-materialized resource: must come back unknown.
output "future_id" {
  value = stub_cert.future.id
}
