terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.6.0"
    }
  }
}

# Two google provider configurations naming two projects. The fence is the
# project (GitHub issue #1121), so this estate cannot be fenced as one.
#
# No google resource is declared: without provider schemas lint refuses
# every google type as unadmitted-type, and this fixture must trip exactly
# one rule. A resource's own project argument is counted the same way;
# internal/live/lint's TestGCPMultiProject covers that half.
provider "google" {
  project = "limits-app"
  region  = "europe-west1"
}

provider "google" {
  alias   = "logs"
  project = "limits-logs"
  region  = "europe-west1"
}
