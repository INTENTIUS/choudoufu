# Clean-pass fixture: the toggle turned on. Selects stock OpenTofu's own
# behavior for a no-source instance - plan a create - instead of the
# default refusal.

terraform {
  live {
    estate = "my-estate"
    strict {
      no_source_create = "create"
    }
  }
}
