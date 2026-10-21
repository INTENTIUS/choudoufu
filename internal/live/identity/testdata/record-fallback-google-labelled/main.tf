# google_recaptcha_enterprise_key's own shape: a ratified GoogleTable row
# whose identity embeds a name the API assigns at create time, so no
# instance resolves from configuration, and a labels map with
# effective_labels beside it, so every instance carries tofu-estate and
# tofu-address in its labels. With a record_store declared it must still
# resolve needs-discovery, bound by its label: see
# [TestRecordFallbackLeavesLabelledGoogleTypeToItsLabel].
terraform {
  live {
    estate = "record-fallback-google"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "google_recaptcha_enterprise_key" "site" {
  display_name = "site"
  project      = "proj-a"
}
