variable "provider_settings" {
  type = any
}

# The live leaf inside a LOCAL of this module rather than at the call, which
# is the hop [rebuildConstructor] cannot reach: there is no constructor at
# the module call to rebuild, and the reference is refused inside this
# module's own scope before anything sees it.
resource "aws_subnet" "inner" {
  vpc_id     = "vpc-11111111"
  cidr_block = "10.0.1.0/24"
}

module "net" {
  source = "../net"
}

locals {
  configuration = merge({
    label                = var.provider_settings["label"]
    public_subnet_id     = var.provider_settings["public_subnet_id"]
    inner_subnet_id      = aws_subnet.inner.id
    },
  module.net.configuration)
}

output "configuration" {
  value = local.configuration
}
