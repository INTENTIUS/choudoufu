variable "cidr" { type = string }

resource "aws_vpc" "this" {
  count      = 1
  cidr_block = var.cidr
  plain_cidr = var.cidr
}

output "passthrough" { value = var.cidr }

output "via_optcomp" { value = try(aws_vpc.this[0].cidr_block, null) }

output "via_plain" { value = try(aws_vpc.this[0].plain_cidr, null) }

output "via_impure" { value = uuid() }

output "via_sensitive" {
  value     = var.cidr
  sensitive = true
}

output "via_two" { value = "${var.cidr},10.78.0.0/16" }
