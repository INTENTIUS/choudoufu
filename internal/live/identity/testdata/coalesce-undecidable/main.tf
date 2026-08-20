# The refusals. Each of these is an argument this package cannot decide,
# and every one of them must refuse rather than fall through to the next
# argument - a fall-through past an argument that turns out to be set
# builds the marker from the wrong expression entirely.

resource "random_pet" "suffix" {
  length = 2
}

module "child" {
  source = "./child"

  name       = "${random_pet.suffix.id}-svc"
  bare_ref   = random_pet.suffix.id
  secret     = "shh"
}
