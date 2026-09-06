# Clean-pass fixture: the toggle turned on. Selects stock OpenTofu's own
# behavior for a resource whose provider configuration changed - plan the
# create under the new one and leave the old one's object where it is -
# instead of the default refusal.

terraform {
  live {
    estate = "my-estate"
    strict {
      provider_change = "recreate"
    }
  }
}
