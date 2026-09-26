# GitHub issue #1371: estate "app" reads a value estate "network" recorded
# at its last apply. The data block is the declaration: it names the other
# estate, the same name render-policy.sh's --reads-outputs-of takes, and the
# outputs it reads. Nothing is listed.
terraform {
  live {
    estate = "app"

    record_store "local" {}
  }
}

data "terraform_estate_outputs" "network" {
  estate = "network"
  names  = ["namespace"]
}

output "namespace" {
  value = data.terraform_estate_outputs.network.values.namespace
}
