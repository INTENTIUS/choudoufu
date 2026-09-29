terraform {
  live {
    estate = "my-estate"
    strict {
      secrets = "ssm"
      ssm {
        kms_key_id = "arn:aws:kms:eu-west-1:111122223333:key/1234abcd"
      }
      ssm {
        kms_key_id = "arn:aws:kms:eu-west-1:111122223333:key/5678efgh"
      }
    }
  }
}
