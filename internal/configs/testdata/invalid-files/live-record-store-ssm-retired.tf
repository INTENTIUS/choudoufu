terraform {
  live {
    estate = "my-estate"

    record_store "ssm" {
      key_prefix = "custom/prefix"
      region     = "us-west-2"
    }
  }
}
