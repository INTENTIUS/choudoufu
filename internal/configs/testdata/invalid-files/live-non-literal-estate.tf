variable "name" {
  type = string
}

terraform {
  live {
    estate = var.name
  }
}
