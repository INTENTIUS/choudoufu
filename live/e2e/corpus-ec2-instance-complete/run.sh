#!/usr/bin/env bash
set -uo pipefail

# terraform-aws-modules/terraform-aws-ec2-instance examples/complete (tag v6.4.0), the most-downloaded module on the registry; absent from the measurement set until 2026-08-23
# Source: https://github.com/terraform-aws-modules/terraform-aws-ec2-instance.git at v6.4.0
#
# Gauntlet crossing script. Each stage below reports through
# live/e2e/lib/gauntlet.sh; see live/GAUNTLET.md for what each stage must
# prove and how it is compared with stock OpenTofu. Replace every
# `gauntlet_stage <id> not_run` with the real check as you implement it;
# a stage left not_run shows as such on the site rather than as a pass.
#
# Env overrides, same as every other crossing:
#   TOFU_BIN      a prebuilt choudoufu binary; skips `go build`.
#   TF_COLD_BIN   the stock binary for the cold deploy (default: terraform).
#   FLOCI_PORT    host port for the emulator (pick one no other script uses).
#   FLOCI_IMAGE   the emulator image; defaults to the digest in live/floci-image.
#   BREAK         set to 1 to corrupt an assertion and prove it is load-bearing.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"
gauntlet_begin

# TODO: start the emulator, copy the estate out, pin the provider, as
# live/e2e/corpus-vpc-complete/run.sh does.

# 1. Cold deploy: The estate is real and buildable: the stock binary applies the unmodified configuration against the emulator, with no live block and no choudoufu involved.
gauntlet_stage cold_deploy not_run

# 2. Migrate: `choudoufu live-import -approve` against the stock state file binds every instance: each state entry becomes a marker on the resource, a record, or an identity derived from the declaration, and the summary line reports zero skipped.
gauntlet_stage migrate not_run

# 3. Replan from nothing: With the state file deleted, `choudoufu live-plan` is empty, and a representative set of rendered identities equals what the AWS CLI reports for the same objects.
gauntlet_stage test_plan not_run

# 4. No-op apply: Applying the empty plan changes nothing: the estate's tagged-object count before and after is identical.
gauntlet_stage test_apply not_run

# 5. Drift and reconverge: One live object is mutated out of band through the AWS CLI; the next plan proposes fixing exactly that object and nothing else, and apply reconverges it.
gauntlet_stage drift_reconverge not_run

# 6. Rename: Renaming a resource through a `moved` block and through `choudoufu live-mv` both produce zero churn: no destroy, no create, the marker rewritten in place.
gauntlet_stage day2_rename not_run

# 7. Remove a block: Deleting a resource block destroys the object under the default policy, in an order the cloud accepts, including blocks for untaggable children whose parents stay.
gauntlet_stage day2_remove not_run

# 8. Change count: Scaling a `count` block down and back up destroys and creates only the instances stock would, and every surviving instance keeps its identity.
gauntlet_stage day2_count not_run

# 9. Replace with create_before_destroy: A forced replacement under `create_before_destroy` creates the new object, destroys the old one, and the next plan is empty with no marker collision.
gauntlet_stage day2_replace not_run

# 10. Crash between create and destroy: A replace interrupted after the create and before the destroy is recovered by the next plan without a human: the old object is destroyed, the new one is bound.
gauntlet_stage day2_crash not_run

# 11. Teardown: `choudoufu apply -destroy` removes every object the estate owns in one apply, in an order the cloud accepts, and leaves nothing marked.
gauntlet_stage day2_teardown not_run

# 12. Plan, review, apply: `plan -out` followed by `apply <planfile>` applies when the world has not moved and refuses, naming the mismatch, when it has.
gauntlet_stage plan_approval not_run

# 13. Greenfield apply: Applying the same configuration from an empty account with choudoufu directly, no migration, produces the same objects stock's cold deploy produced, plus markers.
gauntlet_stage greenfield not_run

# 14. Strict profile: With every strict toggle on, the estate is refused for exactly the things the toggles name (secrets stored, markers unrepaired, and so on) with the documented message, and for nothing else.
gauntlet_stage strict not_run

gauntlet_end
