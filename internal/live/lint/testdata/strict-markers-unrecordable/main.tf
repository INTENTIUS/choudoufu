# A selection naming a type whose exported `id` no source proves to be the
# whole of its documented composite import string
# (identity.IDNotProvenWholeTypes). A record written from `id` would hold a
# fragment, and a fragment handed to a later import is a wrong identity, not
# a missing one.
#
# The type is declared nowhere: the selection is a standing instruction, and
# it is refused when it is written rather than the first time a resource of
# that type appears. It also keeps the fixture to one rule.
terraform {
  live {
    estate = "e"
    record_store "local" {}
    strict {
      markers "record" {
        types = ["aws_cognito_user_pool_client"]
      }
    }
  }
}
