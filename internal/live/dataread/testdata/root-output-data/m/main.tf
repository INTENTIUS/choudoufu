variable "suffix" {
  type = string
}

data "test_zone" "current" {
  name = "example.com."
}

output "arn_static" {
  value = "arn:${data.test_zone.current.zone_id}:${var.suffix}"
}
