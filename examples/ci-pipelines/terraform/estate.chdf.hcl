# The live configuration for this root, as a sidecar rather than a `live`
# block inside terraform{}. Both forms are accepted and one root may use
# only one of them; the sidecar is the leading form because stock tooling -
# `terraform validate`, tflint, editors - never reads a file with this
# extension, so adopting live markers adds one file and changes no existing
# line. chant's terraform lexicon detects the estate from either form.
#
# The whole body is the live block's content, with no wrapper block.
estate = "ci-pipelines-example"

# A CI runner is fresh on every run, so the implied local record store would
# be empty every time and every instance would fall back to its marker tags -
# correct, but a slower plan (the foundation's own rule: losing the record
# costs a slower run and nothing else). An `ssm` store is shared, lives under
# IAM, and is what a pipeline should declare. `s3` is the other shared
# backend; `local` is the implied one.
record_store "ssm" {}
