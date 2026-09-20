terraform {
  live {
    estate = "my-estate"

    record_store "local" {
      exec {
        api_version = "client.authentication.k8s.io/v1beta1"
        command     = "aws"
      }
    }
  }
}
