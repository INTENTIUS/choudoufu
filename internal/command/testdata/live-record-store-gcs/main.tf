terraform {
  backend "gcs" {
    bucket = "acme-state"
    prefix = "prod/network"
  }
}

resource "terraform_data" "marker" {}
