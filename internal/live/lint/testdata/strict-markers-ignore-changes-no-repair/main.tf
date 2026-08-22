# The selection on its own. The resource holds its identity in a record and
# carries no marker, so nothing is lost by ignoring its tags - and the
# refusal stands anyway, because the operator has not said they want this
# tool to stop reconciling marker tags. An ignore_changes = [tags] written
# for an unrelated reason must not silently acquire a new meaning because a
# selection happened to cover the resource.
terraform {
  live {
    estate = "e"
    record_store "local" {}
    strict {
      markers "record" {
        addresses = ["aws_vpc.main"]
      }
    }
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  lifecycle {
    ignore_changes = [tags]
  }
}
