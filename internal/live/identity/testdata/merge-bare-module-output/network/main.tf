variable "name_prefix" {
  type = string
}

output "configuration" {
  value = {
    name_prefix = var.name_prefix
    zone        = "eu-west-1a"
  }
}
