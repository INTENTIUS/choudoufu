# GitHub issue #349's shape: a root output whose value reaches, directly or
# through a module output, a reference into a resource block that provably
# produces ZERO instances.
#
# A real plan resolves each of these to its try() alternative, because its
# own graph expanded the configuration and knows instance 0 does not exist,
# which makes the index a genuine HCL error try() recovers from. The
# evaluation graph core.Eval builds knows only what the state it is handed
# carries, so a zero-instance block and a not-yet-materialized one are the
# same cty.DynamicVal to it - not an error try() can catch - and before the
# fix every output below was left unset and rendered as newly created on
# every single live-plan run.

variable "create_layer" {
  type    = bool
  default = false
}

variable "layer_names" {
  type    = map(string)
  default = {}
}

# count resolves to 0 from configuration and variables alone.
resource "stub_cert" "layer" {
  count = var.create_layer ? 1 : 0
  names = ["layer.example.com"]
}

# for_each resolves to an empty map from configuration and variables alone.
resource "stub_cert" "each_layer" {
  for_each = var.layer_names
  names    = [each.value]
}

# A resource with a real instance in state, so the fall-through alternative
# below has something concrete to land on.
resource "stub_cert" "cert" {
  count = 1
  names = ["example.com"]
}

# A data source that also provably produces zero instances: the same
# question asked in the other resource mode.
data "stub_lookup" "absent" {
  count = var.create_layer ? 1 : 0
  name  = "absent"
}

# NEGATIVE CONTROL for the soundness rule in [identity.ZeroInstanceBlocks].
# This count is not resolvable from configuration alone - it reads a
# resource attribute - so the block gets NO husk and any output reading it
# stays unknown and unset. A real plan does resolve it (to zero, after the
# refresh), so this output is one choudoufu leaves out rather than answers.
# That asymmetry is the point: an unresolvable count read as "zero" would
# put a confidently wrong prior value in front of the diff.
resource "stub_cert" "unknowable" {
  count = stub_cert.cert[0].id == "never-matches" ? 1 : 0
  names = ["unknowable.example.com"]
}

# The module-qualified shape, which is the one corpus-lambda-simple actually
# has: the root output reads a module output, and the module output is a
# try() over a zero-instance block inside the child module.
module "layer" {
  source = "./layer"
}

output "layer_id" {
  value = try(stub_cert.layer[0].id, "")
}

output "each_layer_id" {
  value = try(stub_cert.each_layer["a"].id, "")
}

# try() has to fall PAST the zero-instance data source and land on the real
# resource's own attribute, not stop at the first alternative. This is
# lambda_cloudwatch_log_group_arn's exact shape.
output "log_group_arn" {
  value = try(data.stub_lookup.absent[0].id, stub_cert.cert[0].id, "")
}

# The whole-block reference, with no index: an empty collection, not an
# unknown one, so length() answers.
output "layer_count" {
  value = length(stub_cert.layer)
}

output "module_layer_id" {
  value = module.layer.layer_id
}

output "unknowable_id" {
  value = try(stub_cert.unknowable[0].id, "fell-through")
}
