# The must-not-fire side. Each reference here names something that is not a
# single value this resolver can stand behind, and each must keep refusing
# rather than be quietly resolved by the module-output walk.

module "shard" {
  source   = "./shard"
  for_each = { a = "one", b = "two" }
  label    = each.value
}

module "single" {
  source = "./shard"
  label  = "solo"
}

# A repeated call referenced WHOLE: module.shard.name is an object keyed by
# every instance, not one name.
resource "aws_iam_role_policy" "whole_repeated" {
  name = "p1"
  role = module.shard.name
}

# A key the call does not expand to. "c" is not in the for_each map, so this
# instance will never exist and its identity must not be invented.
resource "aws_iam_role_policy" "missing_key" {
  name = "p2"
  role = module.shard["c"].name
}

# An index on a call that has no repetition at all.
resource "aws_iam_role_policy" "indexed_unrepeated" {
  name = "p3"
  role = module.single[0].name
}

# An output the child module does not declare.
resource "aws_iam_role_policy" "no_such_output" {
  name = "p4"
  role = module.single.nonexistent
}

# The isolating case for the expansion-key check: the output is a literal,
# so every other layer would happily resolve it even though "c" names no
# instance of module.shard.
resource "aws_iam_role_policy" "missing_key_literal_output" {
  name = "p5"
  role = module.shard["c"].constant
}

# The isolating case for the whole-module check: a literal output again, so
# only the repetition guard itself stands between "module.shard.constant"
# (an object keyed by "a" and "b") and a single confident value.
resource "aws_iam_role_policy" "whole_repeated_literal_output" {
  name = "p6"
  role = module.shard.constant
}
