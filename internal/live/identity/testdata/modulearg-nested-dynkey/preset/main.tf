variable "refs" {
  type = map(string)
}

# Keyed on the leaf's VALUE, not on the caller's own key. `keys(local.keyed)`
# is unknowable however much of the rest of the argument is written down.
locals {
  keyed = {
    for name, ref in var.refs : ref => { port = 80 }
  }
}

module "inner" {
  source = "../inner"

  rules = merge(
    local.keyed,
    {},
  )

  # A SET, whose ELEMENTS are its own for_each keys, built from the same
  # leaf. cty leaves a set containing an unknown element with an unknown
  # length, so accepting it would collapse two instances into one address.
  names = toset([for name, ref in var.refs : ref])
}
