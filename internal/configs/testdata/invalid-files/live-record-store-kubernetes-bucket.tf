terraform {
  live {
    estate = "my-estate"

    record_store "kubernetes" {
      bucket = "my-records-bucket"
    }
  }
}
