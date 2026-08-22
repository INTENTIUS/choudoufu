# Limits fixture: RuleStrictMarkersUnrecordable (GitHub issue #365).
#
# The selection in `strict { markers "record" { ... } }` withholds an
# ownership marker and puts the resource's identity in the estate's record
# store instead. That trade is only available for a resource the record can
# actually identify, and internal/live/identity.SelectedLocatedRefusal is the
# three conditions it has to meet - none of which an operator's choice may
# skip, because skipping one produces a wrong identity rather than a missing
# marker, and a wrong identity is invisible to every verdict-level check.
#
# aws_ami_copy is here because it fails the first of the three with no
# provider schema in hand: the provider does not support importing it
# (identity.NotImportableTypes, derived from the provider's own
# documentation). A located record exists so that a LATER run can import the
# object back from it, so a type that cannot be imported cannot be served by
# any record - the marker is the only handle it will ever have.
#
# The type is named in the "types" list and declared nowhere, which is
# deliberate: the selection is a standing instruction, and it is refused when
# it is written rather than the first time a resource of that type is added.
# It also keeps this fixture to exactly one rule, since no resource block
# means no other rule has anything to say.
#
# The other two conditions are schema reads and are proven against real
# hashicorp/aws schemas in internal/live/lint's TestStrictMarkersRefusesAn
# UnrecordableIdentity rather than here: a limits fixture runs through
# CheckContext, which holds no schemas.
#
# See live/LIMITATIONS.md, "strict-markers-unrecordable".

terraform {
  live {
    estate = "my-estate"
    record_store "local" {}
    strict {
      markers "record" {
        types = ["aws_ami_copy"]
      }
    }
  }
}
