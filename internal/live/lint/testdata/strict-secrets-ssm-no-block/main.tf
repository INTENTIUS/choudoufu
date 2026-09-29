# Refusal fixture: secrets = "ssm" with nothing saying which key encrypts
# the parameters. There is no default to fall back on - SSM's alias/aws/ssm
# is readable by every principal in the account holding ssm:GetParameter,
# which is no narrower than the read on the bucket these values are being
# moved out of.

terraform {
  live {
    estate = "my-estate"
    record_store "s3" {
      bucket = "my-records-bucket"
    }
    strict {
      secrets = "ssm"
    }
  }
}
