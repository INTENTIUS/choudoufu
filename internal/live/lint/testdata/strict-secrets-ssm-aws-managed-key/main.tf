# Refusal fixture: the clean arrangement with SSM's own AWS managed key in
# kms_key_id. The spelling alone says it is AWS managed, so it is refused at
# plan time, before an apply creates anything whose record the first-contact
# contract would then refuse to write (GitHub issue #2393).

terraform {
  live {
    estate = "my-estate"
    record_store "s3" {
      bucket = "my-records-bucket"
    }
    strict {
      secrets = "ssm"
      ssm {
        kms_key_id = "alias/aws/ssm"
        path       = "/choudoufu/my-estate/secrets"
      }
    }
  }
}
