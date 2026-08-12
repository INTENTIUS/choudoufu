variable "on" {
  type = bool
}

terraform {
  live {
    estate    = "my-estate"
    snapshots = var.on
  }
}
