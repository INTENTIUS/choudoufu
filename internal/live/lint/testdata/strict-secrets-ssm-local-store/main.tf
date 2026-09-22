# Refusal fixture: the arrangement is complete except for the store. A local
# directory has no conditional write, so nothing would decide a race between
# two writers: the loser would keep a record naming a parameter the winner
# had already deleted.

terraform {
  live {
    estate = "my-estate"
    record_store "local" {
      path = "records"
    }
    strict {
      secrets = "ssm"
      ssm {
        kms_key_id = "arn:aws:kms:eu-west-1:111122223333:key/1234abcd"
      }
    }
  }
}
