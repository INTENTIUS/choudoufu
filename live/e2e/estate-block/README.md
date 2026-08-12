# P4.3 estate-block fixture

A second, smaller estate, standing next to `live/e2e/estate/` (P0.1) for
one reason: it carries a `terraform { live { estate = "..." } }` block,
and the main estate must not.

## Why this is a separate directory, not a flag on the main estate

The contract is that plain `choudoufu plan`/`choudoufu apply` go
stateless only when a configuration's `terraform` block contains a
`live` block — never behind a CLI flag, so a team cannot fall back to a
state file by forgetting one. The main estate's `standup` step
(`live/e2e/run.sh`, step 2) needs the opposite: a stock `choudoufu apply`
that writes a plain `terraform.tfstate`, which is what `adopt` (step 3) then
deletes to demonstrate the "nothing but a marker sweep gets you back" claim.
Adding a `live` block to `live/e2e/estate/` would turn `standup`'s
own apply stateless and it would stop producing a state file, breaking the
demo that step exists for. So the two fixtures live apart: `estate/` proves
adoption from a stock state file, `estate-block/` proves the config-block
path plain plan/apply take once there is no state file to begin with.

## Subset chosen

Full duplication of the main estate's coverage table would mean re-declaring
every admission path (parent-derived routes, named singleton children,
attachment composites, the conditional idiom, the receipt) a second time for
no new claim — P4.3 exercises plain `plan`/`apply` behavior, not admission
coverage, which the main estate and `internal/live/lifecycle`'s
integration test already prove. The subset kept is the smallest one that
still exercises all three marker shapes stamping has to write (bare marker,
client-named, count slot) and stays clean under the two documented emulator
gaps (`chant/test/floci-gaps.md` #5 IAM role tags, #10 SSM receipt
`value_wo`) by construction, not by tolerance:

| Element | Resource(s) | Why kept |
|---|---|---|
| Marker path (server IDs) | `aws_vpc.main`, `aws_subnet.app`, `aws_security_group.main` | The admission path stamping has to write a bare `tofu-estate`/`tofu-address` pair onto, with nothing else in config to derive identity from. |
| Client-named path | `aws_s3_bucket.data`, `aws_cloudwatch_log_group.app` | Identity is already in config; stamping still has to add the marker tags, since none are hand-written here (unlike the main estate). |
| Fungible count | `aws_eip.pool` (`count = 2`) | Proves slot markers (`tofu-slot`) get stamped on a plain apply, not only through `live-plan`/`live-mv`. |

Deliberately excluded, and why each one would have reintroduced a tolerance
step 11 does not want:

- **`aws_iam_role`** — `chant/test/floci-gaps.md` #5: `iam:GetRole` omits
  `Tags`, so any import/refresh-based read of the role shows a
  tags-only in-place diff. Excluding the role keeps step 11's clean-plan
  assertion (`live/e2e/run.sh`, step 11.3) a genuinely empty plan
  instead of routing it through `assert_full_estate_clean`'s tolerance.
- **`aws_ssm_parameter` (the receipt)** — `chant/test/floci-gaps.md` #10:
  SSM drops inline tags, with the same shape of tolerated-but-not-clean
  diff. Excluded for the same reason as the role.
- **`aws_s3_bucket_policy`, `aws_route*`, `aws_iam_role_policy_attachment`,
  the conditional-idiom log group** — parent-derived/named-singleton-child/
  attachment-composite/conditional-idiom coverage the main estate already
  proves; adding them here would duplicate assertions, not add new ones.

No hand-written `tofu-estate`/`tofu-address` tags appear anywhere in this
fixture's `.tf` files (contrast `live/e2e/estate/`, where every
resource carries them from day one) — every marker a `choudoufu apply` here
produces comes from stamping, which is itself part of what step 11 checks.

## Estate name and identifier separation

Every identifier in this fixture (`stateless-e2e-block` estate name, VPC/
subnet CIDRs, bucket name, log group name, security group name) is distinct
from the main estate's (`stateless-e2e`), so the two can stand up in the same
floci account without either estate's plan seeing the other's resources —
step 11 (`live/e2e/run.sh`) asserts exactly that: the main estate's
resource counts are unchanged before and after this fixture's apply.

## Teardown

This fixture has no `apply -destroy` story in v0 (`-destroy` is a named
rejection under a `live` block). Step 11 tears it down via the AWS CLI at
the end of its run — the v0 teardown story.
