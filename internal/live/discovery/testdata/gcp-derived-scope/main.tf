# Derived per-type lists (INTENTIUS/choudoufu#2264): where each declared
# instance of a type listed through its own service is listed, read from its
# block and the provider block.

terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

resource "google_compute_disk" "data" {
  name    = "data"
  project = "proj-b"
  zone    = "projects/proj-b/zones/us-east1-b"
}

resource "google_compute_disk" "boot" {
  name = "boot"
}

resource "google_kms_key_ring" "kr" {
  name     = "kr"
  location = "global"
}

resource "google_kms_crypto_key" "pinned" {
  name     = "pinned"
  key_ring = "projects/proj-a/locations/global/keyRings/kr"
}

resource "google_kms_crypto_key" "referenced" {
  name     = "referenced"
  key_ring = google_kms_key_ring.kr.id
}

resource "google_artifact_registry_repository" "unlocated" {
  repository_id = "r"
  format        = "DOCKER"
}

resource "google_storage_bucket" "logs" {
  name     = "logs"
  location = "US"
}
