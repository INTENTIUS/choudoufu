variable "picked" {
  type = list(string)
}

terraform {
  live {
    estate = "my-estate"
    strict {
      markers "record" {
        types = var.picked
      }
    }
  }
}
