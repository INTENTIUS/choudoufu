variable "how" {
  type    = string
  default = "never"
}

terraform {
  live {
    estate = "my-estate"
    strict {
      marker_repair = var.how
    }
  }
}
