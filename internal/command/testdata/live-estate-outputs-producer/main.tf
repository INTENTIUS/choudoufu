# GitHub issue #1371: estate "network" records two root outputs at every
# apply, which another estate may read through data
# "terraform_estate_outputs". The terraform_data block is there so a
# -target run has something to name.
terraform {
  live {
    estate = "network"

    record_store "local" {}
  }
}

resource "terraform_data" "anchor" {
  input = "network"
}

output "namespace" {
  value = "cluster-services"
}

output "zone" {
  value = "internal.example"
}
