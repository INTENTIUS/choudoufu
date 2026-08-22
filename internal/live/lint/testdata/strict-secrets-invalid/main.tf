# A secrets setting outside the vocabulary altogether: the typo case, and
# the only shape RuleStrictSecrets has. Both settings this fork's schema
# defines are implemented, so there is no grammar-without-a-mechanism case
# here the way marker_repair has one.

terraform {
  live {
    estate = "my-estate"
    strict {
      secrets = "none"
    }
  }
}
