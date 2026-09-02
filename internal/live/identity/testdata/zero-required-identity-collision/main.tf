# Reliably present is not enough on its own: the value has to tell the two
# instances apart. Both blocks write the same name, so both resolve to one
# live certificate and one of them would adopt the other's object.

resource "google_cert" "one" {
  name   = "shared-cert"
  domain = "example.com"
}

resource "google_cert" "two" {
  name   = "shared-cert"
  domain = "api.example.com"
}
