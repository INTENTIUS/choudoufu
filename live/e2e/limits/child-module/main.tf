# Limits fixture: RuleChildModule.
#
# Stateless mode v0 covers the root module only. Every module block below is
# refused, but not for the same reason - #59 narrows this rule module call by
# module call rather than all at once, and this fixture carries the three
# shapes a module call can take so all three reasons are exercised in one
# load:
#
#   - "network" is a static call (no count, no for_each). Refused today
#     because nothing downstream of lint walks into a child module yet
#     (issue #59, phase 2, in progress).
#   - "counted" sets count. Refused permanently: count expansion renumbers
#     every resource address inside the module, which is exactly what a
#     tofu-address marker records.
#   - "keyed" sets for_each. Refused today because nothing downstream of
#     lint walks into a module's instances yet (issue #59, phase 3, planned
#     after the static traversal lands).
#
# See live/LIMITATIONS.md, "child-module".
#
# The root module itself is inside the subset: the only issues this fixture
# raises are the three module calls below.
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

module "counted" {
  source = "./counted"
  count  = 1
}

module "keyed" {
  source   = "./keyed"
  for_each = toset(["a"])
}
