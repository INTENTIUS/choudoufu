# The rename-withholding guard's module-qualified twin (issue #316).
#
# Three copies of one shape - a for_each'd resource block whose declared key
# set no longer contains the key a live marker carries - reached through
# three different module paths:
#
#   - the root module, which is the only path the guard ever worked on;
#   - a static (unkeyed) module call, module.net;
#   - a count'd module call, module.counted[0], the shape issue #195 admitted
#     and internal/live/stamp writes "module.counted[0]..." markers for.
#
# Every one of them is the same question - "does this resource block still
# have a declared instance nothing has claimed?" - and the answer has to be
# the same for all three, or a rename inside a module silently becomes a
# destroy and a create.

module "net" {
  source = "./child"
}

module "counted" {
  source = "./child"
  count  = 1
}

resource "aws_subnet" "this" {
  for_each = toset(["b"])

  vpc_id     = "vpc-00000000000000000"
  cidr_block = "10.0.0.0/24"
}
