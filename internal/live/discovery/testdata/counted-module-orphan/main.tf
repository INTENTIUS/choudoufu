# A module call with count = 1: the shape issue #195 admitted and
# live/e2e/limits/child-module/counted ships. internal/live/stamp resolves the
# count itself and writes "module.counted[0].aws_vpc.<name>" onto every
# resource inside, so a marker read back off one of those objects carries a
# count INDEX in its module step.
#
# The child declares one resource and the test's orphan names another, so the
# orphan is the ordinary "the block was deleted" case that removal planning
# exists for - reached with a module-qualified marker rather than a root one.

module "counted" {
  source = "./child"
  count  = 1
}
