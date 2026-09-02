# The shape the early return was actually right about: an account-wide
# singleton whose identity schema requires nothing for import and offers
# nothing but context. There is no value here that distinguishes one
# instance from another, because there is only ever one.

resource "google_settings" "account" {
  enabled = "true"
}
