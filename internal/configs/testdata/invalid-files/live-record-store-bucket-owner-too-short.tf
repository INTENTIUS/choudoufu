terraform {
  live {
    estate = "my-estate"

    record_store "s3" {
      bucket       = "my-records-bucket"
      bucket_owner = "11112222333"
    }
  }
}
