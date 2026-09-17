terraform {
  live {
    estate = "my-estate"
    retry {
      max_attempts = 10
      mode         = "adaptive"
    }
  }
}
