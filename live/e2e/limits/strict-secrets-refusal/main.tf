# Limits fixture: RuleLogicalResource, the strict block's own
# secrets = "refuse" setting (GitHub issue #365 ruling 5's own principle),
# not the invalid-value shape live/e2e/limits/strict-secrets tests.
#
# random_password is classified SECRET_REFUSED (hashicorp/random 3.9.0
# marks bcrypt_hash and result sensitive) and STORED by default the way a
# stock OpenTofu state file stores it: with a live block present, its
# implied local record store admits this exact resource under the default
# secrets setting. Setting secrets = "refuse" is the one thing that
# refuses it, and the Detail names the setting exactly - see
# internal/live/lint/limits_test.go's TestStrictSecretsRefusalToggleIsTheObstacle
# for the mutation check: the identical resource, with the strict block
# below removed, resolves clean.

terraform {
  live {
    estate = "my-estate"
    strict {
      secrets = "refuse"
    }
  }
}

resource "random_password" "db" {
  length = 16
}
