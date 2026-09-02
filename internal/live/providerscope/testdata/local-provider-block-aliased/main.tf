# Same shape as ../local-provider-block, aliased: the child's own local
# block carries an alias, and the resource inside names it explicitly.
# Resolve must still terminate at the child, keeping the alias, rather than
# looking for a root block named "east" (there is none here).

provider "aws" {
  region = "us-west-2"
}

module "compute" {
  source = "./child"
}
