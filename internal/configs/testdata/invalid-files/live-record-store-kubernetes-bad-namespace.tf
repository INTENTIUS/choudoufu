terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      namespace = "Tofu_Records"
    }
  }
}
