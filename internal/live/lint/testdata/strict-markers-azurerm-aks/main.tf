# GitHub issue #2135: AKS copies a cluster's tags over those of the existing
# route table, public IP and virtual network it is given, so an estate holds
# their ownership in the record store instead. azurerm_dns_cname_record's
# identity schema names dns_zone_name and record_type, which its block does
# not declare; since GitHub issue #2138 the record reads them from where
# azurerm's identity test says they are (zone_name, and the constant
# "CNAME"). azurerm_private_dns_a_record is the control: azurerm's identity
# test finds its record_type only inside the object's own id, which no
# argument supplies, so a record still cannot hold its whole identity.
# Declared nowhere, as in strict-markers-unrecordable: the selection is a
# standing instruction.
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
  }
  live {
    estate = "e"
    record_store "local" {}
    strict {
      markers "record" {
        types = ["azurerm_route_table", "azurerm_public_ip", "azurerm_virtual_network", "azurerm_dns_cname_record", "azurerm_private_dns_a_record"]
      }
    }
  }
}
