# A sensitive value beside an ordinary one, in the same for_each source.
#
# "alice" is a marked string. It must not become each.value and so must not
# become this group's name, because a name is the identity, an identity is the
# tofu-address marker, and a marker is written to a cloud tag in plaintext.
# "carol" is an ordinary string in the same object and must still resolve, so
# that the refusal is shown to be about the mark rather than about the block.
#
# "bob" keeps the whole expression from evaluating as one value, which is
# what routes this through the key-set chase. Without it the object
# evaluates whole and takes the unchanged path, where cty's own marks travel
# with the value.
#
# The block is aws_iam_group, not aws_iam_user as this fixture read before
# GitHub issue #289: aws_iam_user is taggable and enumerable, so its own
# marker fallback would now answer "Identity derived from a sensitive
# value" for it too - correctly, since [resolver.markerFallback] never
# builds an import ID from the sensitive value, it just defers to the
# marker - but that is not what THIS fixture pins. It pins the mark never
# reaching the identity in the first place, which is what "alice" not
# resolving at all, still, keeps proving. aws_iam_group carries no tags
# argument and stays outside that gate.
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

resource "aws_iam_group" "team" {
  for_each = local.members

  name = each.value
}
