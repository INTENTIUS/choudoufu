# Static scope for the staticForEachKeyNames shape table: locals a key
# expression can be built from, plus one resource so the directory is a
# loadable module.
locals {
  pick    = true
  dup_key = "alice"
  base    = { alice = "a" }
}

resource "aws_iam_user" "anchor" {
  name = "anchor"
}
