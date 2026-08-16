# The bar the zero-required rule has to clear: an attribute that is optional
# for import AND optional in the block is only an identity component when
# this configuration writes it on every instance. Here one block does not,
# so the provider will mint that certificate's name and no marker-free run
# can find it again.

resource "google_cert" "named" {
  name   = "front-cert"
  domain = "example.com"
}

resource "google_cert" "unnamed" {
  domain = "api.example.com"
}
