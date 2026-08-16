# Deliberately no `terraform { required_providers { aws = {
# configuration_aliases = [...] } } }` here: that declaration makes OpenTofu's
# own config loader require an explicit providers entry at the immediate
# call boundary (confirmed by hand - adding it here makes
# testdata/dead-end-alias/main.tf fail to build with "Missing required
# provider configuration" before Resolve ever runs), so a config that
# reaches this module with the alias genuinely unpassed can only be built
# without it: an ad hoc `provider = aws.primary` reference, resolved by
# inheritance rather than a formal requirement.

resource "aws_s3_bucket" "data" {
  provider = aws.primary

  bucket = "dead-end-bucket"
}
