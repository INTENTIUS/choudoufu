# Server-named google types (INTENTIUS/choudoufu#2134): Vertex AI assigns
# each index endpoint's name at create time, so no instance resolves from
# configuration and every one is found by its labels through Cloud Asset
# Inventory. keys is a fungible count set, the shape tofu-slot exists for.
# (The type is one whose Asset name its importer takes as it is,
# INTENTIUS/choudoufu#2139 item 7.)

terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

resource "google_vertex_ai_index_endpoint" "site" {
  display_name = "site"
  project      = "proj-a"
  region       = "us-central1"
}

resource "google_vertex_ai_index_endpoint" "keys" {
  count = 2

  display_name = "pool"
  project      = "proj-a"
  region       = "us-central1"
}
