# A data source's attribute feeding an identity argument, the corpus's
# dominant data-source idiom (#179 stage 1): resolution alone refuses this
# as a dynamic value, and resolution handed the phase's read result
# resolves the exact identity, with the value read from the provider and
# never inferred.
data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.aws_route53_zone.primary.zone_id}"
}

locals {
  # The same value reached through a local: coverage must hold transitively,
  # because that is how the corpus's real configurations reach it.
  zone_id = data.aws_route53_zone.primary.zone_id
}

resource "aws_cloudwatch_log_group" "via_local" {
  name = "/zones-local/${local.zone_id}"
}
