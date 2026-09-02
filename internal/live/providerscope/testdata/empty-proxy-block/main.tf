# internal/configs/provider_validation.go's "could be a proxy
# configuration" shape: the child's own provider block below is empty, so
# it carries no divergent settings of its own, and the call may use count
# even though the module declares its own block
# (provider_validation.go:592's error only fires for a content-bearing one).
# Resolve must fall through this empty block to root's real configuration,
# not stop at the child and read its empty body as if it were authoritative
# - the child's own settings, if it silently used them, would be an
# unconfigured (all-null) provider instead of root's real us-west-2 one.

provider "aws" {
  region = "us-west-2"
}

module "compute" {
  source = "./child"
  count  = 1
}
