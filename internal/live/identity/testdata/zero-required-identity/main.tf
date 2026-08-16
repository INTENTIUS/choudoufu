# Four types whose provider requires NOTHING for import, plus one that
# requires something, all read from the same schema set.
#
# google_compute_managed_ssl_certificate and google_workflows_workflow are
# the real shapes these fakes are transcribed from: an identity schema whose
# every attribute is optional-for-import, because the ambient half (project,
# and for some types the region) can be defaulted and the provider's import
# ID grammar puts them all in one string. The name is still the only thing
# that tells one certificate from another, and every block writes it.

resource "google_cert" "primary" {
  name   = "front-cert"
  domain = "example.com"
}

resource "google_cert" "secondary" {
  name   = "back-cert"
  domain = "api.example.com"
}

# region is Optional and not Computed in this type's own block, so the
# schema itself says the provider will not fill it in: it joins the name.
resource "google_flow" "backups" {
  name   = "nightly-backups"
  region = "europe-west2"
  source = "main.yaml"
}

# The control: its identity schema requires topic_id, so nothing about this
# one goes near the zero-required rule.
resource "google_topic" "events" {
  topic_id = "events"
}
