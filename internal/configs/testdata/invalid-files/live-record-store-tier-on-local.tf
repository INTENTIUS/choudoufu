terraform {
  live {
    estate = "my-estate"

    record_store "local" {
      tier = "advanced"
    }
  }
}
