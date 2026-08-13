variable "verb" {
  type = string
}

terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged = var.verb
    }
  }
}
