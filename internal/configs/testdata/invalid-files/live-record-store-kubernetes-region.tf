terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      region = "us-west-2"
    }
  }
}
