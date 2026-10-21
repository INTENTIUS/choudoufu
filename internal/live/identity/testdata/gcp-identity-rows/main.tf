# One resource of each shape identity.GoogleTable rows (tools/gcp-row-gen,
# hashicorp/google 8.6.0), resolved with no provider schemas. The provider
# block states project, region and zone; the google provider substitutes
# them into an id format when a resource names none of its own, and the
# rows read them from here the same way.

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
  zone    = "europe-west1-b"
}

# A provider block that states no project: a resource under it that names
# none either cannot be named here, and needs discovery.
provider "google" {
  alias  = "bare"
  region = "europe-west1"
}

# Global: project from the provider block.
resource "google_compute_network" "vpc" {
  name                    = "estate-vpc"
  auto_create_subnetworks = false
}

# Regional: project and region from the provider block.
resource "google_compute_subnetwork" "app" {
  name          = "app"
  ip_cidr_range = "10.0.0.0/24"
  network       = google_compute_network.vpc.id
}

# Zonal, with the resource's own project and zone overriding the provider's.
resource "google_compute_disk" "data" {
  project = "estate-data"
  zone    = "europe-west1-c"
  name    = "data"
  size    = 10
}

# A topic, labelled: the google-labels surface, tier A.
resource "google_pubsub_topic" "events" {
  name   = "events"
  labels = { team = "platform" }
}

# A service account: the id names the computed email, which the provider's
# own CustomizeDiff derives from account_id and project.
resource "google_service_account" "runner" {
  account_id = "ci-runner"
}

# A minted identity on a labelled type: needs discovery, found by marker.
resource "google_recaptcha_enterprise_key" "site" {
  display_name = "site"
  labels       = { team = "platform" }
  web_settings {
    integration_type  = "SCORE"
    allow_all_domains = true
  }
}

# IAM members, identified through their parent.
resource "google_project_iam_member" "runner_logs" {
  project = "estate-prod"
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.runner.email}"
}

resource "google_storage_bucket_iam_member" "reader" {
  bucket = "estate-artifacts"
  role   = "roles/storage.objectViewer"
  member = "group:readers@example.com"
  condition {
    title      = "until 2027"
    expression = "request.time < timestamp(\"2027-01-01T00:00:00Z\")"
  }
}

# No project anywhere: needs discovery.
resource "google_pubsub_topic" "orphan" {
  provider = google.bare
  name     = "orphan"
}
