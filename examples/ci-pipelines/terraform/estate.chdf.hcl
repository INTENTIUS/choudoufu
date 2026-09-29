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
# costs a slower run and nothing else). A bucket is shared, lives under IAM,
# and is what a pipeline should declare. `local` is the implied store.
#
# The bucket is not created by anything here. It is stood up once, by hand,
# with examples/record-store-bucket (`AWS_REGION=us-east-1 just up`, whose
# derived name for this account and region is the one below), and
# scripts/oidc-bootstrap.sh reads the name from this file to write the apply
# role's policy. A bucket name is global, so a fork of this example changes
# this line. Until GitHub issue #1346 this was `record_store "ssm" {}`;
# Parameter Store is retired as a record store.
# Missing on purpose, for one release: bucket_owner = "354867293429".
#
# A bucket name is global, and this one has an account id in it, so anyone who
# reads this file knows what to create in their own account if the real bucket
# ever stops existing. bucket_owner is what puts ExpectedBucketOwner on every
# request and makes a bucket of this name somewhere else unusable by this
# estate (GitHub issue #1381).
#
# It is not here yet because the workflows this example generates pin a
# released binary (v0.18.0), and a released binary does not know the argument:
# it would refuse this whole configuration as an unsupported one and break the
# example's real pipelines. Add the line in the pin-bump PR after the next
# release.
#
# Until then the policy half covers this bucket. scripts/oidc-bootstrap.sh
# renders the apply role's policy with --account, so every Allow in it
# requires aws:ResourceAccount, and its head-bucket carries
# --expected-bucket-owner.
record_store "s3" {
  bucket = "choudoufu-records-354867293429-us-east-1"
}
