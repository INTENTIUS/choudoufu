terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.6.0"
    }
  }

  # 64 characters: a legal estate name (up to 128), and one character over
  # GCP's 63-character label value. Every google resource carries the
  # estate as its tofu-estate label, so none of them can be stamped.
  live {
    estate = "limits-gcp-estate-label-a-name-one-character-past-sixty-three-xy"
  }
}

provider "google" {
  project = "limits-app"
  region  = "europe-west1"
}

# Without provider schemas lint also refuses google_storage_bucket as
# unadmitted-type; internal/live/lint's limitsCompanionRules says why that
# rule is allowed beside this one.
resource "google_storage_bucket" "named" {
  name     = "limits-gcp-estate-label"
  location = "EU"
}
