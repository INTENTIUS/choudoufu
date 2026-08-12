variable "path" {
  type = string
}

terraform {
  live {
    estate        = "my-estate"
    snapshot_path = var.path
  }
}
