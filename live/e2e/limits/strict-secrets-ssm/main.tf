# Limits fixture: RuleStrictSecretsSSM (GitHub issue #1515).
#
# `secrets = "ssm"` is a setting this fork's schema defines, spelled
# correctly, and this estate still cannot run under it. Two things it needs
# are missing, and each one is refused by name:
#
#   - No `ssm` block, so nothing says which KMS key encrypts the parameters.
#     There is no default to fall back on: SSM's own alias/aws/ssm key is
#     readable by every principal in the account holding ssm:GetParameter,
#     which is no narrower than the read on the bucket these values would be
#     moving out of. Defaulting to it would move the secrets and protect
#     nothing.
#
#   - No `record_store "s3"`, so this estate's records go to the implied
#     local directory. The whole of ruling 1 is that the record's own
#     compare-and-swap decides a concurrent write: each secret value is
#     created at a parameter name no other write uses, and the record's
#     conditional write is what commits the reference to it. A local
#     directory has no conditional write, so the loser of a race would keep
#     a record naming a parameter the winner had already deleted, and that
#     secret would be gone.
#
# Both are refused at the configuration, before the first parameter exists,
# rather than at the store, where a run has already written one. See
# live/LIMITATIONS.md, "strict-secrets-ssm".

terraform {
  live {
    estate = "my-estate"
    strict {
      secrets = "ssm"
    }
  }
}
