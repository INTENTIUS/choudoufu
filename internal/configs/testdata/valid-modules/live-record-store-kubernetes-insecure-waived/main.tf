terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      namespace      = "tofu-records-my-estate"
      host           = "https://cluster.example:6443"
      insecure       = true
      allow_insecure = ["tls_verification"]
    }
  }
}
