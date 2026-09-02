# Slice 1 of #189's follow-up: a module CALL has its own for_each, and a
# resource inside the child module reaches the call's each.key/each.value
# only indirectly, through var.name - the shape namedDef (localvalue.go)
# used to decline outright. name is built from BOTH each.key and
# each.value.role, so a leak of one instance's repetition data into
# another's resolution would show up as a wrong identity, not a refusal.

locals {
  users = {
    alice = { role = "admin" }
    bob   = { role = "reader" }
  }
}

module "user" {
  source   = "./user"
  for_each = local.users

  name = "${each.key}-${each.value.role}"
}
