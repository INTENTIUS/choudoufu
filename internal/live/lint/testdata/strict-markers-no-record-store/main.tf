# A selection with nowhere to put the identities it moves. The limits wing
# carries this shape too (live/e2e/limits/strict-markers); this copy is here
# so the lint matrix reads in one place.
terraform {
  live {
    estate = "e"
    strict {
      markers "record" {
        types = ["aws_vpc"]
      }
    }
  }
}
