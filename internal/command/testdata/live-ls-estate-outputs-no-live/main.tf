# GitHub issue #1859's true refusal: no live block, so no record store, so
# terraform_estate_outputs has nothing to read through and live-ls must not
# invent one.
data "terraform_estate_outputs" "network" {
  estate = "network"
  names  = ["namespace"]
}

output "namespace" {
  value = data.terraform_estate_outputs.network.values.namespace
}
