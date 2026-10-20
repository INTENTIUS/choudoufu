# Server-named google types (INTENTIUS/choudoufu#2134): reCAPTCHA Enterprise
# assigns each key's name at create time, so no instance resolves from
# configuration and every one is found by its labels through Cloud Asset
# Inventory. keys is a fungible count set, the shape tofu-slot exists for.

terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

resource "google_recaptcha_enterprise_key" "site" {
  display_name = "site"
  project      = "proj-a"
}

resource "google_recaptcha_enterprise_key" "keys" {
  count = 2

  display_name = "pool"
  project      = "proj-a"
}
