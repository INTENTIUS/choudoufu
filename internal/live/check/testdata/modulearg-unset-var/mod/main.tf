variable "buckets" {
  type = map(string)
}

locals {
  # The same conditional-around-a-comprehension the ECS module writes, so the
  # key-set chase cannot prove a branch and the tolerant retry is the last
  # thing standing between this and a fabricated instance key.
  selected = var.buckets != null ? { for k, v in var.buckets : k => v } : {}
}

resource "aws_s3_bucket" "this" {
  for_each = local.selected

  bucket = each.value
}
