# A selection naming a type whose exported `id` no source proves to be the
# whole of its documented composite import string
# (identity.IDNotProvenWholeTypes). A record written from `id` would hold a
# fragment, and a fragment handed to a later import is a wrong identity, not
# a missing one.
#
# The subject moved off aws_cognito_user_pool_client when
# tools/importdocs-gen learned to read its page's possessive-of import
# sentence: that type now carries an identity.DocumentedImportIDs grammar, so
# a record CAN hold its whole identity and it is no longer an example of this
# refusal. aws_glue_partition is chosen because the reason it stays refused
# is a documented shape rather than a gap: its page names one segment as the
# prose phrase "partition values", and tools/row-gen/docimportid.go's own
# doc comment states outright that a segment named by a phrase rather than a
# token is refused, because turning a description into a name is a guess.
# No widening of the scrape reaches it without withdrawing that rule.
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
        types = ["aws_glue_partition"]
      }
    }
  }
}
