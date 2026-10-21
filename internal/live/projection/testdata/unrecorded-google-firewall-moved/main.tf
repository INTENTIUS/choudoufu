# corpus-k8s-infra-gcp-gcve's day2_rename shape: a markerless
# google_compute_firewall renamed by a chain of two moved blocks. Its record
# was written under the first address and nothing re-keys a record for a
# bare HCL moved block, so the record the instance has is the one at a
# moved-from address.

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

moved {
  from = google_compute_firewall.ssh-v1
  to   = google_compute_firewall.ssh_v2
}

moved {
  from = google_compute_firewall.ssh_v2
  to   = google_compute_firewall.ssh
}
