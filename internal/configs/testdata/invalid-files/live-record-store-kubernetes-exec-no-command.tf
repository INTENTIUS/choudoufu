terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      exec {
        api_version = "client.authentication.k8s.io/v1beta1"
      }
    }
  }
}
