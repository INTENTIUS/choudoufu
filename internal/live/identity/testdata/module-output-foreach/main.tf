# The per-instance correctness case. A for_each'd module call, each
# instance's output reachable only with its own key, and a dependent
# resource per key. If the module hop carried the wrong instance's scope -
# or resolved every key against one instance - both policies would come
# back with the same identity, which is the "wrong marker over a for_each
# map" defect this repository has shipped once before. Distinct expected
# values are the whole point of the fixture.

module "shard" {
  source   = "./shard"
  for_each = { blue = "b-role", green = "g-role" }

  label = each.value
}

resource "aws_iam_role_policy" "blue" {
  name = "p"
  role = module.shard["blue"].name
}

resource "aws_iam_role_policy" "green" {
  name = "p"
  role = module.shard["green"].name
}
