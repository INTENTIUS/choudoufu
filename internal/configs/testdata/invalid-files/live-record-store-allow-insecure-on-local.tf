terraform {
  live {
    estate = "my-estate"

    record_store "local" {
      allow_insecure = ["versioning"]
    }
  }
}
