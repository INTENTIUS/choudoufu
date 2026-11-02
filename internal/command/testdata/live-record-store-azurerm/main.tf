terraform {
  backend "azurerm" {
    resource_group_name  = "rg-state"
    storage_account_name = "acmestate"
    container_name       = "tfstate"
    key                  = "prod/network/terraform.tfstate"
  }
}

resource "terraform_data" "marker" {}
