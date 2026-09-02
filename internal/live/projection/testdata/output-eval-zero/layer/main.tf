# The child half of the module-qualified shape: this is where
# terraform-aws-lambda's own optional Lambda Layer sits, and where its
# seven `try(aws_lambda_layer_version.this[0].<attr>, "")` outputs are
# written. The zero-instance block a root output has to see through is in
# a module, not beside it.

variable "create_layer" {
  type    = bool
  default = false
}

resource "stub_cert" "this" {
  count = var.create_layer ? 1 : 0
  names = ["module-layer.example.com"]
}

output "layer_id" {
  value = try(stub_cert.this[0].id, "")
}
