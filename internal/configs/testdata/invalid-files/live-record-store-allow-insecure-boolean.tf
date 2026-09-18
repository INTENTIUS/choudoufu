terraform {
  live {
    estate = "my-estate"

    record_store "s3" {
      bucket         = "my-records"
      allow_insecure = true
    }
  }
}
