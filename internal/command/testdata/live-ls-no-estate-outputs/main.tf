# GitHub issue #1859: a live block and no terraform_estate_outputs block.
# live-ls opens no record store for this configuration.
terraform {
  live {
    estate = "app"

    record_store "local" {}
  }
}

output "greeting" {
  value = "hello"
}
