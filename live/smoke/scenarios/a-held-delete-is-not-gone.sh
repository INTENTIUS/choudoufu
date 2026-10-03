# a-held-delete-is-not-gone
# CLAIM 25 (aws) - A delete the platform accepted but has not finished is neither forgotten nor repeated: the plan after it reads the object the way the provider does - an object the provider still reads as present keeps its marker, the sweep finds it, and every plan proposes the same one destroy until it is really gone; an object the provider reads as gone plans nothing, as stock's plan does, with no second delete and no refusal. ~3 min.
#
# GitHub issue #1599. The AWS form of a held delete is a scheduled one:
# Secrets Manager's DeleteSecret with a recovery window, like KMS's
# ScheduleKeyDeletion, answers success and leaves the resource in the
# account - DescribeSecret still returns it, with a DeletedDate and its
# tags - until the window ends. That is "accepted but not finished" in the
# plainest sense, and the emulator produces it.
#
# What differs from Kubernetes is the provider's reading of it.
# hashicorp/aws 6.58.0's findSecretByID
# (internal/service/secretsmanager/secret.go) returns NotFound when
# DeletedDate is set, and its delete waits on exactly that, so the provider
# itself calls the scheduled secret gone; KMS's findKeyByID does the same
# for PendingDeletion. So the half of the claim this scenario measures is
# the second one: the plan agrees with the provider, as stock's does. It
# must not propose a second destroy - a DeleteSecret on a secret already
# scheduled for deletion fails, and a plan that proposed it on every run
# would wedge the estate for the whole recovery window - and it must not
# refuse the swept-but-absent object as a contradiction, the #596 refusal
# (projection/sighted.go's undeclared case is what keeps it out).
#
# The first half - an object the provider still reads as present after its
# own delete returned - has no AWS instance the emulator can produce: the
# AWS shape of it is an eventually consistent delete, which floci
# deliberately does not emulate (its tagging service says so), and most
# hashicorp/aws deletes wait the lag out anyway. That is why the AWS cell of
# claim 25 reads "restated", not "proven".
#
# BREAK=1 sets recovery_window_in_days = 0, which the provider sends as
# ForceDeleteWithoutRecovery, and requires the opposite outcome: the secret
# really gone, DescribeSecret answering ResourceNotFoundException, and the
# replan still empty. Without it, the main arm's "the secret is still
# there" would read the same if choudoufu never sent the delete at all.
# The control's destroy runs on a block already removed from the
# configuration, so its window of 0 reaches the provider only through the
# estate's residue record (projection/residue.go); a control that fails
# with the secret still present after its destroy is that record not
# reaching a destroy, and is a finding in its own right.

SCEN="a-held-delete-is-not-gone"
SMOKE_WORK="$SMOKE_WORKROOT/$SCEN"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK

ESTATE="smoke-held"
NAME="smoke-held-secret"
if [ "${BREAK:-0}" = "1" ]; then WINDOW=0; else WINDOW=7; fi

cat > "$SMOKE_WORK/versions.tf" <<EOF
terraform {
  required_version = ">= 1.5.0"
  live {
    estate = "$ESTATE"
  }
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}
provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
}
EOF
# Two secrets, so the destroy below is one of two and the plan after it has
# a live, declared neighbour to read: an empty plan must be an empty plan
# over a real estate, not over nothing.
cat > "$SMOKE_WORK/main.tf" <<EOF
resource "aws_secretsmanager_secret" "kept" {
  name = "smoke-held-kept"
}

resource "aws_secretsmanager_secret" "held" {
  name                    = "$NAME"
  recovery_window_in_days = $WINDOW
}
EOF

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

# described prints "<DeletedDate or None> <tofu-estate or None>" for the
# held secret, or NOTFOUND when DescribeSecret says it does not exist.
described() {
  local out
  if out="$(awsl secretsmanager describe-secret --secret-id "$NAME" \
        --query '[DeletedDate, Tags[?Key==`tofu-estate`]|[0].Value]' --output text 2>&1)"; then
    printf '%s\n' "$out"
  elif grep -q ResourceNotFoundException <<< "$out"; then
    echo NOTFOUND
  else
    printf 'ERROR %s\n' "$out"
  fi
}

step "the claim"
explain \
  "Some AWS deletes are requests. A Secrets Manager secret deleted with a" \
  "recovery window is scheduled, not removed: the API answers success" \
  "and the secret stays, tags and all, until the window ends. The" \
  "provider reads a scheduled secret as gone, and so does stock. The" \
  "promise here is that the plan after the delete says what the provider" \
  "says - nothing to do - rather than proposing a second delete AWS would" \
  "refuse, or refusing an object its own sweep can still see."

step "1. the estate applies"
cmd "choudoufu init && choudoufu apply -auto-approve"
logged a-held-delete-is-not-gone-init "$SCEN" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY1="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "$SCEN" "apply failed: $APPLY1"
grep -E 'Apply complete!' <<< "$APPLY1" | evidence
grep -qE 'Apply complete! Resources: 2 added' <<< "$APPLY1" || fail "$SCEN" "the first apply did not report two added: $APPLY1"
D1="$(described)"
echo "$NAME: $D1" | evidence
[ "$D1" = "None"$'\t'"$ESTATE" ] || fail "$SCEN" "the held secret is not live and marked tofu-estate=$ESTATE after the apply: $D1"
proof "two secrets, both marked; the held one carries recovery_window_in_days = $WINDOW."

step "2. the block is deleted from source - one destroy, found by the marker"
cmd "remove resource \"aws_secretsmanager_secret\" \"held\" ; choudoufu plan"
python3 - "$SMOKE_WORK/main.tf" <<'PYEOF' || fail "$SCEN" "could not remove the held block"
import re, sys
p = sys.argv[1]
s = open(p).read()
t = re.sub(r'\nresource "aws_secretsmanager_secret" "held" \{.*?\n\}\n', '\n', s, flags=re.S)
if t == s:
    raise SystemExit(1)
open(p, 'w').write(t)
PYEOF
PLAN2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "$SCEN" "plan failed: $PLAN2"
grep -E '^Plan:' <<< "$PLAN2" | evidence
grep -qE 'Plan: 0 to add, 0 to change, 1 to destroy' <<< "$PLAN2" || fail "$SCEN" "the plan is not the one destroy of the removed block: $PLAN2"
proof "exactly one destroy, of the secret the configuration no longer declares."

step "3. apply - the run says destroyed; AWS still holds the secret"
explain \
  "The provider sends DeleteSecret with the recovery window and waits" \
  "until DescribeSecret shows a DeletedDate, which is what it calls gone." \
  "The secret is still in the account, with its marker."
cmd "choudoufu apply -auto-approve ; aws secretsmanager describe-secret --secret-id $NAME"
APPLY3="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "$SCEN" "the destroying apply failed: $APPLY3"
grep -E 'Destruction complete|Apply complete!' <<< "$APPLY3" | evidence
grep -qE 'Resources: 0 added, 0 changed, 1 destroyed' <<< "$APPLY3" || fail "$SCEN" "the apply did not report one destroyed: $APPLY3"
D3="$(described)"
echo "$NAME: $D3" | evidence

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a window of zero; the delete really finishes"
  [ "$D3" = "NOTFOUND" ] \
    || fail "$SCEN" "BREAK: with recovery_window_in_days = 0 the secret is still there after the destroy ($D3). Either the force delete was never sent, or the window of 0 on the removed block did not reach the provider's delete through the residue record; either way the main arm's held secret would not be the recovery window's doing."
  BPLAN="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "$SCEN" "BREAK: the replan failed: $BPLAN"
  grep -q "No changes." <<< "$BPLAN" || fail "$SCEN" "BREAK: the replan after a finished delete is not empty: $(grep -E '^Plan:|will be|^Error' <<< "$BPLAN" | head -3)"
  echo "No changes." | evidence
  proof "caught - with no recovery window the secret is gone from DescribeSecret and the replan is empty, so the secret the main arm finds after the same destroy is held by its window, not left by a delete choudoufu never sent."
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

case "$D3" in
  NOTFOUND|ERROR*|None*) fail "$SCEN" "the secret is not held after the destroy - DescribeSecret says $D3 - so there is no accepted-but-unfinished delete to measure" ;;
esac
[ "$(cut -f2 <<< "$D3")" = "$ESTATE" ] || fail "$SCEN" "the scheduled secret lost its marker: $D3"
proof "the run counts one destroyed; AWS holds the secret with a DeletedDate and tofu-estate=$ESTATE until the window ends."

step "4. the plan after it - nothing, twice, with no second delete and no refusal"
explain \
  "The provider reads the scheduled secret as gone. The plan must agree:" \
  "no destroy proposed again, which AWS would refuse for as long as the" \
  "window runs, no create, and no refusal of an object the estate's own" \
  "marker still sits on."
for n in 1 2; do
  cmd "choudoufu plan   # $n of 2"
  PLAN4="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "$SCEN" "plan $n after the scheduled delete failed: $(grep -E '^Error' -A3 <<< "$PLAN4" | head -6)"
  grep -q "No changes." <<< "$PLAN4" || fail "$SCEN" "plan $n after the scheduled delete is not empty: $(grep -E '^Plan:|will be|^Error' <<< "$PLAN4" | head -3)"
  grep -q "^Error" <<< "$PLAN4" && fail "$SCEN" "plan $n carries an error: $(grep -E '^Error' <<< "$PLAN4" | head -2)"
  echo "plan $n: No changes." | evidence
done
[ "$(cut -f2 <<< "$(described)")" = "$ESTATE" ] || fail "$SCEN" "the scheduled secret changed under the plans: $(described)"
proof "No changes, twice, over a secret AWS still holds with this estate's marker - the plan reads it as the provider does."

step "5. teardown"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "$SCEN" "teardown failed"
awsl secretsmanager delete-secret --secret-id "$NAME" --force-delete-without-recovery >/dev/null 2>&1 || true
proof "the estate is gone, and the held secret with it."
