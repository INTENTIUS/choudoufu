# Limits fixture: RuleChildModule.
#
# This fixture carries the five shapes a module call can take, and two of
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
#   - "counted" sets count to a statically evaluable literal, and none of
#     its own arguments read count.index. Admitted (issue #195): a plain
#     integer count is not positionally fragile the way this fixture's
#     comments used to claim - module.name[i] is exactly as stable an
#     address as resource.name[i], and shrinking count only ever retires
#     the highest index, never renumbers a survivor. RuleChildModule
#     reports nothing for it, and the five walkers traverse
#     "module.counted[0]...".
#   - "counted-leaking" also sets a statically evaluable count, but one of
#     its own arguments (suffix = var.suffixes[count.index]) indexes into a
#     collection at count.index. Still refused: issue #192 narrowed
#     count.index handling to admit a pure-scalar use (a bare argument, a
#     template, "100 + count.index") but not an index into a collection,
#     because what sits at position count.index is controlled by the
#     collection, not by the index - reorder or shrink var.suffixes and a
#     later instance's marker would point at the wrong live value. This is
#     that same guard ([checkCountIndex], resource bodies) applied to a
#     module call's own arguments instead.
#   - "keyed" sets for_each over an expression that reads another resource's
#     attribute, which is not knowable from configuration alone. Refused for
#     the same reason a resource's own non-static for_each is: an instance
#     key becomes part of an address, and an address that is not known yet
#     cannot become part of a marker yet.
#
# See live/LIMITATIONS.md, "child-module".
#
# The root module itself is inside the subset, and so are "network",
# "keyed-static" and "counted"; the only issues this fixture raises are the
# "counted-leaking" and "keyed" module calls.
#
# Unlike every other fixture in this wing, this one needs "choudoufu get"
# before lint can be reached at all: a module block is refused with "Module
# not installed" while the configuration is still being loaded, which is
# earlier than any stateless code runs. The harness does that one step for
# this directory; nothing else about it is special.

variable "suffixes" {
  type    = list(string)
  default = ["a", "b"]
}

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

module "counted-leaking" {
  source = "./counted-leaking"
  count  = 2
  suffix = var.suffixes[count.index]
}

module "keyed" {
  source   = "./keyed"
  for_each = toset([aws_s3_bucket.data.bucket])
}
