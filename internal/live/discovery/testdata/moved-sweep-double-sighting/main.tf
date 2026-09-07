# Fixture for the corpus-overture-tiles day2_rename regression: a declared,
# needs-discovery instance of a type BOTH the config-driven leg and the
# estate-wide sweep leg enumerate in the same pass.
#
# name_prefix, not name: the provider mints the rest of the name at create
# time, so the identity is not derivable from the configuration and the
# instance is ClassNeedsDiscovery - which is what gives it a declaredEntry
# that accumulates claimants. A client-named sibling (name = "...") is
# ClassConcrete, never gets an entry, and cannot show this shape at all.
#
# The moved block is what puts it in both legs at once. The estate's record
# is keyed by the address the instance had before the rename, so the new
# address is not record-backed and the type stays in the config-driven scan
# universe; the sweep universe adds it back because IAM is a service the
# Resource Groups Tagging API does not index (issue #692) and the provider
# serves no list resource for the type (issue #881). One live object, two
# legs, one entry.

resource "aws_iam_instance_profile" "renamed" {
  name_prefix = "estate-"
  role        = "estate-role"
}

moved {
  from = aws_iam_instance_profile.original
  to   = aws_iam_instance_profile.renamed
}
