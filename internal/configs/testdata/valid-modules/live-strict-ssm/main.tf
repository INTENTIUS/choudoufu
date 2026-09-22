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
        region     = "eu-west-1"
      }
    }
  }
}
