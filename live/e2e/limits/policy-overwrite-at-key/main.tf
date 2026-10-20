# Limits fixture: RulePolicyOverwriteAtKey.
#
# "keep" is a valid declared_untagged verb, a quieter refusal: a declared
# object found without this estate's marker stays out of the estate, and
# the plan still proposes the resource's create. On Kubernetes the API
# server refuses that create with 409 (#1546). An azurerm create is a PUT,
# which updates an object already at the declared id instead of failing,
# so the same create would take an unmarked resource group over in silence.
# internal/live/lint/policy.go refuses the verb for an estate that declares
# such a resource. See live/LIMITATIONS.md, "policy-overwrite-at-key"
# (GitHub issue #1120).
#
# Without provider schemas lint also refuses azurerm_resource_group as
# unadmitted-type; internal/live/lint's limitsCompanionRules says why that
# rule is allowed beside this one.

terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "5.10.0"
    }
  }
  live {
    estate = "my-estate"
    policy {
      declared_untagged = "keep"
    }
  }
}

provider "azurerm" {
  features {}
}

resource "azurerm_resource_group" "shared" {
  name     = "limits-policy-overwrite-at-key"
  location = "westeurope"
}
