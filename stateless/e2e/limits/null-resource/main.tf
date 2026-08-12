# Limits fixture: RuleLogicalResource, null_resource with triggers.
#
# A null_resource has no existence outside the record OpenTofu keeps of it —
# the triggers map is state used to decide when to re-run something, and that
# record is exactly the store stateless mode removes. See
# stateless/LIMITATIONS.md.

resource "null_resource" "trigger" {
  triggers = {
    input = "value"
  }
}
