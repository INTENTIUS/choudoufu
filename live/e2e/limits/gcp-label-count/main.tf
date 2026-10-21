terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.6.0"
    }
  }
}

provider "google" {
  project = "limits-app"
  region  = "europe-west1"

  # Off, so the bucket's own labels are the whole stock count.
  add_terraform_attribution_label = false
}

# 63 labels: stock creates this bucket, and GCP's cap is 64. The two
# markers this run adds (tofu-estate, tofu-address) make 65, so the create
# would fail only because of them.
#
# Without provider schemas lint also refuses google_storage_bucket as
# unadmitted-type; internal/live/lint's limitsCompanionRules says why that
# rule is allowed beside this one.
resource "google_storage_bucket" "crowded" {
  name     = "limits-gcp-label-count"
  location = "EU"
  labels   = { for i in range(63) : "label-${i}" => "x" }
}
