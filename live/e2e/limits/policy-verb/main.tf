# Limits fixture: RulePolicyVerb.
#
# "delete" is not a verb this fork allows in the declared+tagged quadrant.
# Those are the resources the configuration names and this estate already
# owns: converging them is the ordinary run, and deleting them on the
# strength of a policy setting would make a configuration change into a
# destroy nobody asked for. See internal/live/policy.ValidVerbs for the
# matrix and live/LIMITATIONS.md, "policy-verb".

terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged = "delete"
    }
  }
}
