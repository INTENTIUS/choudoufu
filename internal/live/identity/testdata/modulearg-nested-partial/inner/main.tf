variable "rules" {
  type    = any
  default = {}
}

# The key set. Every key is written in the configuration two module calls
# up, so every instance here has an address the configuration states, and
# the identity reads the key and nothing else.
resource "aws_iam_user" "keyed" {
  for_each = var.rules

  name = "user-${replace(each.key, "/", "-")}"
}

# The literal sibling leaf, carried through the caller's own merge() and a
# setproduct over two stated key sets. `port` is written in ./preset's own
# default; nothing about it depends on the leaf that cannot be evaluated.
resource "aws_iam_group" "sibling" {
  for_each = var.rules

  name = "group-${each.value.port}"
}

# The refusing half, and the reason the two above are worth having. This
# identity reads the ONE leaf the configuration does not state. It must
# render nothing at all - in particular not the key, not the port, and not
# any literal that happens to sit beside it in the same object.
resource "aws_iam_role" "dynamic" {
  for_each = var.rules

  name = each.value.ref
}
