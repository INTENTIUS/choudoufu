# #252 shape A: exactly the module-foreach-var fixture next door, so the
# ONLY difference between the two is whether the child module's reference to
# var.name is bare or wrapped in a function call. name is built from BOTH
# each.key and each.value.role, so one instance seeing another's repetition
# data shows up as a wrong identity ("alice-reader"), not as a refusal.

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
