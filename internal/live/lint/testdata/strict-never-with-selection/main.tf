# marker_repair = "never" with a selection to give it a mechanism. Accepted:
# "never"'s whole observable content is that lifecycle { ignore_changes } over
# the marker tags stops being refused for the resources the selection covers,
# and this block names some.
#
# No resource declares ignore_changes here, so nothing else has anything to
# say either. There are also no provider schemas in this run, which is what
# makes the point worth pinning: whether the selection can be HONOURED needs
# a schema, but whether it exists does not, and it is existence that decides
# whether "never" has a mechanism to name.
terraform {
  live {
    estate = "e"
    record_store "local" {}
    strict {
      marker_repair = "never"
      markers "record" {
        types = ["aws_vpc"]
      }
    }
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
