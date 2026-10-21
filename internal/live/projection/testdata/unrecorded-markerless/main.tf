# corpus-k8s-infra-gcp-gcve's day2_count shape: a google_project_service per
# for_each key, a type with no labels map and so no marker surface, whose
# identity comes out of this file. The maintainer's ruling of 2026-10-20
# decides what one of these with no record means.

terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

resource "google_project_service" "project" {
  for_each = toset(["compute.googleapis.com", "essentialcontacts.googleapis.com"])

  project = "broadcom-451918"
  service = each.key
}
