# Id formats whose {{location}} the schema marks optional and computed, and
# one whose id format names a placeholder the schema lacks
# (tools/gcp-row-gen location.go, hashicorp/google 8.6.0, #2145 item 7).

terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.6.0"
    }
  }
}

provider "google" {
  project = "estate-prod"
  region  = "europe-west1"
}

# The repository's encoder sets an unset location from
# tpgresource.GetRegion: the provider's region here.
resource "google_artifact_registry_repository" "defaulted" {
  repository_id = "images"
  format        = "DOCKER"
}

# Written, the location is the repository's own.
resource "google_artifact_registry_repository" "multi" {
  repository_id = "mirror"
  location      = "us"
  format        = "DOCKER"
}

# Nothing in the rule's Create sets location: written, it names the rule.
resource "google_artifact_registry_rule" "keep" {
  repository_id = "images"
  rule_id       = "keep"
  location      = "europe-west1"
}

# Left out, the Create's URL and id carry an empty segment, so this one
# instance is refused by name.
resource "google_artifact_registry_rule" "nowhere" {
  repository_id = "images"
  rule_id       = "nowhere"
}

# {{name}} is no attribute and no provider field, so ReplaceVars renders it
# empty in the create's id and the importer's alike.
resource "google_compute_snapshot_settings" "settings" {
  storage_location {
    policy = "NEAREST_MULTI_REGION"
  }
}
