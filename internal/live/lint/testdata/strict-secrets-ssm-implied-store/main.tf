# Refusal fixture: no record_store at all. Every live block has an implied
# local one, which is enough for the constructs that have no marker to fall
# back on and is not enough here, for the same reason the declared local
# store is not: no conditional write to commit a reference with.

terraform {
  live {
    estate = "my-estate"
    strict {
      secrets = "ssm"
      ssm {
        kms_key_id = "arn:aws:kms:eu-west-1:111122223333:key/1234abcd"
      }
    }
  }
}
