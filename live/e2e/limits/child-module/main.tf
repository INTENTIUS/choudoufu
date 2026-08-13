# Limits fixture: RuleChildModule.
#
# This fixture carries the four shapes a module call can take, and two of
# them are still refused:
#
#   - "network" is a static call (no count, no for_each). Admitted (issue
#     #59, phase 2 / 59b): the five walkers traverse it, so its resources
#     bind by module-qualified address exactly as a root resource does.
#     RuleChildModule reports nothing for it.
#   - "keyed-static" sets for_each over a literal set of strings. Admitted
#     (issue #59, phase 3 / 59c): its keys are knowable from configuration
#     alone, so RuleChildModule reports nothing for it either, and the five
#     walkers traverse each instance ("module.keyed-static[\"a\"]...").
#   - "counted" sets count. Refused permanently: count expansion renumbers
#     every resource address inside the module, which is exactly what a
#     tofu-address marker records.
#   - "keyed" sets for_each over an expression that reads another resource's
#     attribute, which is not knowable from configuration alone. Refused for
#     the same reason a resource's own non-static for_each is: an instance
#     key becomes part of an address, and an address that is not known yet
#     cannot become part of a marker yet.
#
# See live/LIMITATIONS.md, "child-module".
#
# The root module itself is inside the subset, and so are "network" and
# "keyed-static"; the only issues this fixture raises are the "counted" and
# "keyed" module calls.
#
# Unlike every other fixture in this wing, this one needs "choudoufu get"
# before lint can be reached at all: a module block is refused with "Module
# not installed" while the configuration is still being loaded, which is
# earlier than any stateless code runs. The harness does that one step for
# this directory; nothing else about it is special.

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-limits-child-module"
}

module "network" {
  source = "./network"
}

module "keyed-static" {
  source   = "./keyed-static"
  for_each = toset(["a", "b"])
}

module "counted" {
  source = "./counted"
  count  = 1
}

module "keyed" {
  source   = "./keyed"
  for_each = toset([aws_s3_bucket.data.bucket])
}
