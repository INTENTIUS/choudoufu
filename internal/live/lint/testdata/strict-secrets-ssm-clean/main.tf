# Clean-pass fixture: the whole arrangement GitHub issue #1515's ruling 2
# asks for. A customer managed KMS key, a parameter path, and the one record
# store whose conditional write can commit a reference.
#
# It is the control for the five refusal fixtures beside it: each of those
# is this file with exactly one thing removed or changed, so a refusal that
# fired here would be firing on something other than what it names.

terraform {
  live {
    estate = "my-estate"
    record_store "s3" {
      bucket = "my-records-bucket"
    }
    strict {
      secrets = "ssm"
      ssm {
        kms_key_id = "arn:aws:kms:eu-west-1:111122223333:key/1234abcd"
        path       = "/choudoufu/my-estate/secrets"
      }
    }
  }
}
