terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      namespace      = "tofu-records-my-estate"
      allow_insecure = ["versioning"]
    }
  }
}
