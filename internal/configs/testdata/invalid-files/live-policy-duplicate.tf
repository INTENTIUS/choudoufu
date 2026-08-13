terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged = "keep"
    }
    policy {
      declared_tagged = "converge"
    }
  }
}
