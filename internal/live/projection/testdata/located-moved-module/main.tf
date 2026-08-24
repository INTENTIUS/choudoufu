# The day2_rename shape: a `moved` block renaming the whole MODULE call, not
# the resource inside it. The record-located resource's own address never
# changes textually - only the module path it is nested under does - which
# is exactly the property [moved.Aliases] already handles for a live marker
# and, before this fixture's fix, did not for a record store lookup.

module "thing_renamed" {
  source = "./child"
}

moved {
  from = module.thing
  to   = module.thing_renamed
}
