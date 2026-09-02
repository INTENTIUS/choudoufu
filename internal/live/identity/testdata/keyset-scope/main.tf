# Static scope for the staticForEachKeyNames shape table: locals a key
# expression can be built from, plus one resource so the directory is a
# loadable module.
locals {
  pick    = true
  dup_key = "alice"
  base    = { alice = "a" }

  # #308's Gap A shape: each entry's VALUE has one attribute (secret) that
  # never proves, and one (active) that always does. A for-comprehension
  # filtering on v.active must read only that attribute and leave secret
  # untouched.
  filtered_entries = {
    alice = { active = true, secret = data.d.x.arn }
    bob   = { active = false, secret = data.d.x.arn }
  }
}

resource "aws_iam_user" "anchor" {
  name = "anchor"
}
