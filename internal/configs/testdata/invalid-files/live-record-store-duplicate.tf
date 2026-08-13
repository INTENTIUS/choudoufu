terraform {
  live {
    estate = "my-estate"

    record_store "local" {
      path = ".tofu-records"
    }

    record_store "ssm" {
    }
  }
}
