# Clean-pass fixture: a strict block that sets nothing at all. Every toggle
# resolves to its default, which is today's behavior, so checkLiveStrict has
# nothing to say.

terraform {
  live {
    estate = "my-estate"
    strict {
    }
  }
}
