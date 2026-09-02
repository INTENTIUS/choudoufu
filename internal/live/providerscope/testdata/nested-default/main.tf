# Two levels of nesting, neither call naming a providers argument at all -
# the overwhelmingly common shape, and the one Resolve must leave exactly
# where it already sits: the root's own default configuration.

provider "aws" {
  region = "us-west-2"
}

module "mid" {
  source = "./mid"
}
