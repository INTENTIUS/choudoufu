# Refusal fixture: an ssm block that names a path and no key. The block is
# there, so an operator reading this configuration would see the
# arrangement; the one argument that makes it worth having is missing.

terraform {
  live {
    estate = "my-estate"
    record_store "s3" {
      bucket = "my-records-bucket"
    }
    strict {
      secrets = "ssm"
      ssm {
        path = "/choudoufu/my-estate/secrets"
      }
    }
  }
}
