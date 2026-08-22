# Limits fixture: RuleStrictMarkers (GitHub issue #365).
#
# `strict { markers "record" { ... } }` is HANDOFF.md's third principle as a
# toggle: "per-type or per-address markers = record, for tag budgets and tag
# policies, trading IAM governability for a record-held identity". The
# selected resources hold their identity in the estate's record store instead
# of in a tofu-address tag, and no marker is written for them at all.
#
# This live block declares no record_store, so there is nowhere for those
# identities to go. That is the refusal: a resource with neither a marker nor
# a record cannot be found by any later run, so the first apply would create
# an object and every plan after it would propose creating another one - the
# "created unfindable" failure HANDOFF.md's safety rule exists to prevent,
# arriving silently, with every plan verdict clean.
#
# The other shapes this rule catches are in internal/live/lint/testdata: an
# empty markers block, an address outside the -target grammar, an address
# naming one instance rather than a resource block, and an address naming a
# resource this configuration does not declare.
#
# See live/LIMITATIONS.md, "strict-markers".

terraform {
  live {
    estate = "my-estate"
    strict {
      markers "record" {
        types = ["aws_ebs_volume"]
      }
    }
  }
}
