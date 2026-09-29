terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      namespace      = "tofu-records-my-estate"
      insecure       = true
      allow_insecure = ["tls_verify"]
    }
  }
}
