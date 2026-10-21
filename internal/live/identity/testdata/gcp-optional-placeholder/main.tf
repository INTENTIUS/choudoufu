# An id format that names an optional argument the schema does not mark
# computed (tools/gcp-row-gen, hashicorp/google 8.6.0): the connection's
# {{location}}. ReplaceVars reads it from configuration and nothing else
# fills it in.

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

# Written, it names the connection before apply.
resource "google_bigquery_connection" "lake" {
  connection_id = "lake"
  location      = "EU"
  cloud_resource {}
}

# Left out, there is no value to name it by: this one instance is refused
# by name rather than rendered with the empty segment stock would write.
resource "google_bigquery_connection" "nowhere" {
  connection_id = "nowhere"
  cloud_resource {}
}
