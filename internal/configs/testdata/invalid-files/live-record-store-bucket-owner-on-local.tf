terraform {
  live {
    estate = "my-estate"

    record_store "local" {
      bucket_owner = "111122223333"
    }
  }
}
