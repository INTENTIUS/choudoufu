locals {
  estate_tag = "synth-composite-e2e"
}

resource "aws_s3_bucket" "holder" {
  bucket = "tofu-synth-composite-e2e"
  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_s3_bucket.holder"
  }
}

# aws_s3_object is in neither generated table (identity nor admission): it
# reaches live mode only through SynthesizeTypeIdentity, from the provider's
# own identity schema, whose required attributes are [bucket, key] - a
# multi-attribute composite with no separator, GitHub issue #105's exact
# shape. With both identity arguments statically present the config signal
# proves client naming, the synthesized entry is IdentityObjectOnly, and the
# replan can only reach the live object through the identity-object import
# path - there is no import-ID string to fall back to, by construction.
# No `content` argument, deliberately. An object's content is write-only
# residue: the provider's Read never fetches the body, so a replan from
# markers proposes re-setting it forever - true of stock `terraform import`
# for this type too, and GitHub issue #73's "record-less residue" question,
# not this test's. What this test isolates is the composite identity path,
# and an empty object exercises it fully.
resource "aws_s3_object" "doc" {
  bucket = aws_s3_bucket.holder.bucket
  key    = "doc.txt"
  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_s3_object.doc"
  }
}
