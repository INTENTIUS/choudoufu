# Issue #375 named this shape as its root cause: a merge() call one of whose
# ARGUMENTS is a bare sibling-module-output reference, reduced from
# uyuni-project/sumaform's own backend_modules/aws/base/main.tf.
#
#   locals { configuration_output = merge({ ... }, module.network.configuration) }
#   module "host" { base_configuration = local.configuration_output }
#
# It is here because it RESOLVES, and resolved before any of #375's work:
# [resolver.selectStatic] already reads a step into a merge() of object
# constructors and chases a module-output reference into the child module, so
# `var.base_configuration["name_prefix"]` inside the child finds its way to
# module.network's own output expression one argument at a time. The fixture
# is the pin that keeps it that way, and the record that the issue's stated
# mechanism was not the one blocking corpus-sumaform-aws.
#
# What actually blocks that estate is a COUNT, which cannot be answered one
# argument at a time - see testdata/module-arg-hoisted, and issue #375's own
# thread for the rest of the chain.
resource "aws_subnet" "public" {
  vpc_id     = "vpc-12345678"
  cidr_block = "10.0.1.0/24"
}

module "base" {
  source = "./base"

  provider_settings = {
    create_network   = true
    public_subnet_id = aws_subnet.public.id
  }
}
