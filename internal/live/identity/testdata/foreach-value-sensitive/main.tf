# A sensitive value beside an ordinary one, in the same for_each source.
#
# "alice" is a marked string. It must not become each.value and so must not
# become this user's name, because a name is the identity, an identity is the
# tofu-address marker, and a marker is written to a cloud tag in plaintext.
# "carol" is an ordinary string in the same object and must still resolve, so
# that the refusal is shown to be about the mark rather than about the block.
#
# "bob" keeps the whole expression from evaluating as one value, which is
# what routes this through the key-set chase. Without it the object
# evaluates whole and takes the unchanged path, where cty's own marks travel
# with the value.
variable "secret" {
  type      = string
  sensitive = true
  default   = "s3cr3t-name"
}

resource "aws_iam_group" "admins" {
  name = "admins"
}

locals {
  members = {
    "alice" = var.secret
    "bob"   = aws_iam_group.admins.name
    "carol" = "carol-from-config"
  }
}

resource "aws_iam_user" "team" {
  for_each = local.members

  name = each.value
}
