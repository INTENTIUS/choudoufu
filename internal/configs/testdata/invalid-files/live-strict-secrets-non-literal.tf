variable "how" {
  type    = string
  default = "refuse"
}

terraform {
  live {
    estate = "my-estate"
    strict {
      secrets = var.how
    }
  }
}
