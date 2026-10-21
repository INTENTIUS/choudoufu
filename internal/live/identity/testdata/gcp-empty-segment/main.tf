# Id formats naming an optional argument with no default that every
# hashicorp/google 8.6.0 import format captures with [^/]*: unset, stock's
# ReplaceVars writes an empty segment there (tools/gcp-row-gen
# emptysegment.go, #2145 item 6).

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

resource "google_compute_global_network_endpoint" "by_ip" {
  global_network_endpoint_group = "neg"
  ip_address                    = "10.0.0.1"
  port                          = 80
}

resource "google_compute_global_network_endpoint" "by_fqdn" {
  global_network_endpoint_group = "neg"
  fqdn                          = "api.example.com"
  port                          = 443
}

# Written as null, the argument is as unset as when it is left out.
resource "google_compute_global_network_endpoint" "null_fqdn" {
  global_network_endpoint_group = "neg"
  ip_address                    = "10.0.0.9"
  fqdn                          = null
  port                          = 80
}

resource "google_compute_region_network_endpoint" "by_ip" {
  region_network_endpoint_group = "rneg"
  ip_address                    = "10.0.0.2"
  port                          = 8080
}

resource "google_compute_region_network_endpoint" "by_fqdn" {
  region_network_endpoint_group = "rneg"
  fqdn                          = "db.example.com"
  port                          = 5432
}

# A non-GCP endpoint names no instance.
resource "google_compute_network_endpoint" "no_instance" {
  network_endpoint_group = "zneg"
  ip_address             = "10.0.0.3"
  port                   = 80
}

# Neither: the provider's AtLeastOneOf rejects it, and so does resolution.
resource "google_compute_global_network_endpoint" "neither" {
  global_network_endpoint_group = "neg"
  port                          = 80
}

# The importer captures name with [^/]+ and the create mints one when it is
# unset, so an unset name is no empty segment: refused.
resource "google_spanner_backup_schedule" "unnamed" {
  instance           = "spanner"
  database           = "db"
  retention_duration = "172800s"
}
