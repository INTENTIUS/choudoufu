# #252 shape A, the collision direction: the call repeats, the child module
# ignores what varies, and both instances therefore claim one live object.
# Resolving the wrapped expression must not cost the duplicate check.

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
