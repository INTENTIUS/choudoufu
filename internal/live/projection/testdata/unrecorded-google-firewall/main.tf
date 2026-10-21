# A google_compute_firewall at a name its configuration chooses: no labels
# map, so no marker surface, and a create at a held name that Google
# refuses with 409 alreadyExists. The 2026-10-20 ruling's name-held case.

terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

resource "google_compute_firewall" "ssh" {
  project = "broadcom-451918"
  name    = "allow-ssh"
  network = "default"
}
