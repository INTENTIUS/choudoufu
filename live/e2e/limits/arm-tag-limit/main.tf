# Limits fixture: an Azure resource with no room for its ownership markers.
#
# Azure's type data documents at most 15 tags on a Cosmos DB account
# (Microsoft.DocumentDB/databaseAccounts; internal/live/armtypes's
# taglimits_generated.txt), though azurerm's own validation allows 50.
# This account declares 14, so stock creates it, and the two markers this
# run adds (tofu-estate, tofu-address) make 16. RuleARMTagLimit
# (internal/live/lint/arm_tag_limit.go) warns, naming both limits and
# their sources. See live/LIMITATIONS.md, "arm-tag-limit".
#
# Without provider schemas lint also refuses azurerm_cosmosdb_account as
# unadmitted-type; internal/live/lint's limitsCompanionRules says why that
# rule is allowed beside this one.

terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "5.10.0"
    }
  }
}

provider "azurerm" {
  features {}
}

resource "azurerm_cosmosdb_account" "crowded" {
  name                = "limits-arm-tag-limit"
  location            = "westeurope"
  resource_group_name = "limits"
  offer_type          = "Standard"

  consistency_policy {
    consistency_level = "Session"
  }

  geo_location {
    location          = "westeurope"
    failover_priority = 0
  }

  tags = { for i in range(14) : "tag-${i}" => "x" }
}
