# Limits fixture: RuleStrictSecretsSSM (GitHub issue #1515).
#
# The reverse mistake, and the one that fails silently. This estate declares
# an ssm block, names a customer managed KMS key, and leaves `secrets` at its
# default - so nothing goes to Parameter Store, every value is written into
# the record in clear exactly as before, and this configuration reads as
# though the key were protecting them.
#
# Ignoring the block would be invisible. The only evidence would be the
# absence of parameters nobody was watching for, which is why the block is
# refused rather than dropped.
#
# The refusals for the other direction - `secrets = "ssm"` with no key, or
# with a record store that has no conditional write to commit a reference
# with - are exercised in internal/live/lint/testdata/strict-secrets-ssm-*.
# They cannot be the fixture here: a configuration that asks for the setting
# also gets "strict-secrets"'s "not implemented yet" refusal, because no
# build writes a parameter yet, and this directory asserts exactly one rule.
# See live/LIMITATIONS.md, "strict-secrets-ssm".

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
