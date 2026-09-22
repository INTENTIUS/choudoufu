# Refusal fixture: the reverse mistake. The block is written, the customer
# managed key is named, and secrets is left at its default - so every value
# goes into the record in clear, exactly as before, while this configuration
# reads as though the key were protecting them.
#
# Ignoring the block would be invisible. The only evidence would be the
# absence of parameters nobody was watching for.

terraform {
  live {
    estate = "my-estate"
    record_store "s3" {
      bucket = "my-records-bucket"
    }
    strict {
      ssm {
        kms_key_id = "arn:aws:kms:eu-west-1:111122223333:key/1234abcd"
      }
    }
  }
}
