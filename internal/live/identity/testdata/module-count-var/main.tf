# namedDef's count counterpart: the module CALL is count-gated, and the
# resource inside reaches count.index only through var.name at the module
# boundary.

module "user" {
  source = "./user"
  count  = 2

  name = "user-${count.index}"
}
