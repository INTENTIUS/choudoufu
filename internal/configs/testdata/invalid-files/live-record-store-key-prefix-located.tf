terraform {
  live {
    estate = "my-estate"

    record_store "ssm" {
      key_prefix = "tofu-located/evil"
    }
  }
}
