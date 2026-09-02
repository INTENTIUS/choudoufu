# GitHub issue #365, slice 2: HANDOFF.md's third principle as a toggle.
#
# aws_ebs_volume is selected, so it holds its identity in the estate's record
# store and no ownership marker is written into its tags. Because it has that
# other identity source, and because marker_repair = "never" says the operator
# wants its tags left alone, lifecycle { ignore_changes = [tags] } on it is
# honoured the way stock honours it instead of being refused.
#
# aws_vpc is not selected, and is here so that a test can assert what did NOT
# move: it is stamped, it resolves through its ordinary route, and its own
# ignore_changes would still be refused. A selection that leaked would show up
# as this resource changing too.
#
# Analyzed WITHOUT provider schemas - which is how the identity golden sweeps
# it - neither resource is selected in effect: identity.SelectedLocatedType
# fails closed with no schema, so aws_ebs_volume resolves through its ordinary
# route and keeps its marker. That is the deliberate direction and
# TestStrictMarkersRecordFailsClosedWithNoSchemas pins it.

terraform {
  live {
    estate = "markers-record"

    record_store "local" {}

    strict {
      marker_repair = "never"

      markers "record" {
        types = ["aws_ebs_volume"]
      }
    }
  }
}

resource "aws_ebs_volume" "selected" {
  availability_zone = "us-east-1a"
  size              = 8

  lifecycle {
    ignore_changes = [tags]
  }
}

resource "aws_vpc" "unselected" {
  cidr_block = "10.42.0.0/16"
}
