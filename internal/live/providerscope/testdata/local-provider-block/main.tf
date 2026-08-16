# GitHub issue #201's actual corpus shape: a child module declares its own,
# content-bearing provider block, reached by a plain module call with no
# count, for_each, enabled or depends_on. Resolve must terminate at the
# child module itself rather than climbing to root's own (different)
# default configuration - if it silently fell through to root, a resource
# in the child would be planned against root's us-west-2 account instead of
# whatever the child's own block, and its own var, name.

provider "aws" {
  region = "us-west-2"
}

module "compute" {
  source = "./child"
  org    = "my-org"
}
