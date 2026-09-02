# The call passes nothing at all - no providers argument, so the child's
# aliased reference has no proxy anywhere in the tree to carry it upward.
# Real OpenTofu's own Inherited() never climbs past an aliased reference
# with no matching entry, so Resolve must stop at the child rather than
# guess a root address by walking further on the same name.

provider "aws" {
  region = "us-west-2"
}

module "child" {
  source = "./child"
}
