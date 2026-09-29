terraform {
  live {
    estate = "my-estate"

    record_store "s3" {
      bucket       = "my-records-bucket"
      bucket_owner = "1234-5678-9012"
    }
  }
}
