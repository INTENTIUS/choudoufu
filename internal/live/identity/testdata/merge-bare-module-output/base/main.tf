variable "provider_settings" {
  type = any
}

locals {
  create_network   = lookup(var.provider_settings, "create_network", false)
  public_subnet_id = lookup(var.provider_settings, "public_subnet_id", null)
}

module "network" {
  source = "../network"

  name_prefix = "demo"
}

locals {
  configuration_output = merge({
    product = "sumaform"
    subnet  = local.public_subnet_id
    },
    module.network.configuration,
  )
}

module "host" {
  source = "../host"

  quantity           = local.create_network ? 1 : 0
  base_configuration = local.configuration_output
}
