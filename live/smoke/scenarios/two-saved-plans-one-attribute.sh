# two-saved-plans-one-attribute
# CLAIM 2 (aws) - Nothing is held. ~4 min.
#
# This proof: Two saved plans, one attribute: when two approved plans
# change the same echoed attribute of one cloud resource, the second apply
# is refused because the value it was planned FROM is gone, and the cloud
# keeps the first apply's value.
#
# GitHub issue #1504, ruling 4. two-writers-one-record races a
# record-backed type, where the record is the whole object and the
# conditional write referees. An ordinary cloud resource's record holds only
# its identity, the arguments the provider never reads back, taint, deposed
# objects and tombstones (internal/live/projection/record.go). An attribute
# the provider reads back from the cloud is not in it, and since #1939 a
# record whose bytes did not change is not written, so no conditional write
# is checked when two applies change such an attribute. What referees two
# saved-plan applies there is #878: `apply <planfile>` re-reads the live
# system, plans again and refuses when one of its changes differs from the
# approved plan in its before- or after-values
# (internal/live/approval/values.go). This scenario proves that half on a
# real emulated resource: an SQS queue's visibility_timeout_seconds, which
# the provider reads back from GetQueueAttributes (step 2 checks that on
# this run rather than assuming it).
#
# Two plain `apply` runs are last-writer-wins on such an attribute, and
# this scenario does not claim otherwise; site/content/docs/model/
# concurrency.md says so. Two saved-plan applies can also both re-read
# before either writes (#1504's gap 3); this scenario applies them one after
# the other, which is the case the before-values check covers.
#
# BREAK=1 rebuilds choudoufu with the before-values comparison removed
# (go build -overlay, as two-writers-one-record does for If-Match), and
# passes only when the second apply is caught reporting success while the
# cloud ends on its value: the silent overwrite.

SMOKE_WORK="$SMOKE_WORKROOT/echorace"
mkdir -p "$SMOKE_WORK/a" "$SMOKE_WORK/b"; export SMOKE_WORK

ESTATE="smoke-echorace"
BUCKET="smoke-echorace-records"
QUEUE="smoke-echorace-work"

# write_config <dir> <visibility timeout>. Checkouts a and b are two copies
# of one estate: same estate name, same record bucket, same queue.
write_config() {
  cat > "$1/versions.tf" <<EOF
terraform {
  required_version = ">= 1.5.0"
  live {
    estate = "$ESTATE"

    record_store "s3" {
      bucket = "$BUCKET"
    }
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
  cat > "$1/main.tf" <<EOF
resource "aws_sqs_queue" "work" {
  name                       = "$QUEUE"
  visibility_timeout_seconds = $2
}
EOF
}

flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }
queue_url() { awsl sqs get-queue-url --queue-name "$QUEUE" --query QueueUrl --output text; }
visibility() { awsl sqs get-queue-attributes --queue-url "$(queue_url)" --attribute-names VisibilityTimeout --query Attributes.VisibilityTimeout --output text; }
set_visibility() { awsl sqs set-queue-attributes --queue-url "$(queue_url)" --attributes "VisibilityTimeout=$1"; }

step "the claim"
explain \
  "Nothing here takes a lock, so two approved plans can reach one cloud" \
  "resource. When both change an attribute the provider reads back from" \
  "the cloud, the record store does not referee: that value lives in the" \
  "cloud, not in the record. The saved plan does. An apply of a plan file" \
  "re-reads the live system and compares what it would do now with what" \
  "was approved, before-values included. The second plan was approved" \
  "against a value the first apply replaced, so it is refused by name and" \
  "the cloud keeps the first apply's value."

# The binary under test. BREAK=1 swaps in one whose approval check never
# compares before-values.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - without the before-values check the second apply must be caught overwriting"
  explain \
    "The corruption is in the binary: the approval comparison drops the" \
    "before side and keeps the after side. Built with go build -overlay," \
    "so the source tree is never touched; it needs this checkout and Go."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "echorace" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "echorace" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/live/approval/values.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/values.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
old = "\tout = append(out, compareSide(\"before\", approved.Before, fresh.Before, approved.BeforeNull, fresh.BeforeNull)...)\n"
assert src.count(old) == 1, "the break patch no longer matches CompareValues"
open(sys.argv[2], "w").write(src.replace(old, "\t// BREAK: the before-values are not compared\n"))
PYEOF
  [ -s "$SMOKE_WORK/break/values.go" ] || fail "echorace" "the break patch did not apply to $SRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/values.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # CompareValues without its before side"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "echorace" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi

step "1. one estate, one queue, two checkouts of it"
stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "echorace" "could not create the record bucket"
awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled >/dev/null
awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >/dev/null
awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null
write_config "$SMOKE_WORK/a" 30
write_config "$SMOKE_WORK/b" 30
logged two-saved-plans-one-attribute-a-init "echorace" "init failed in a" -- in_dir "$SMOKE_WORK/a" "$RUN_BIN" init -input=false -no-color
logged two-saved-plans-one-attribute-b-init "echorace" "init failed in b" -- in_dir "$SMOKE_WORK/b" "$RUN_BIN" init -input=false -no-color
cmd "choudoufu apply -auto-approve   # in checkout a"
OUT="$(cd "$SMOKE_WORK/a" && "$RUN_BIN" apply -auto-approve -input=false -no-color 2>&1)" || fail "echorace" "the seeding apply failed: $OUT"
[ "$(visibility)" = "30" ] || fail "echorace" "the queue's visibility timeout is $(visibility) after the seeding apply, want 30"
echo "VisibilityTimeout=$(visibility)" | evidence
proof "one queue, its visibility timeout 30 seconds in the cloud."

step "2. the attribute is echoed"
explain \
  "The race below is only about the cloud if the provider reads this" \
  "attribute back. So the cloud is changed behind both checkouts, and" \
  "checkout b's plan must see it: a value it can only have read from" \
  "GetQueueAttributes, since nothing it holds was told."
cmd "aws sqs set-queue-attributes VisibilityTimeout=45 ; choudoufu plan   # in checkout b"
set_visibility 45 || fail "echorace" "could not change the visibility timeout out of band"
ECHO_PLAN="$(cd "$SMOKE_WORK/b" && "$RUN_BIN" plan -input=false -no-color 2>&1)" || fail "echorace" "checkout b's plan failed: $ECHO_PLAN"
grep -E 'visibility_timeout_seconds' <<< "$ECHO_PLAN" | head -2 | evidence
grep -qE 'visibility_timeout_seconds += 45 -> 30' <<< "$ECHO_PLAN" \
  || fail "echorace" "checkout b's plan did not read the out-of-band 45 back from the cloud, so this attribute is not echoed and the race below would not be about the cloud: $ECHO_PLAN"
set_visibility 30 || fail "echorace" "could not put the visibility timeout back"
[ "$(visibility)" = "30" ] || fail "echorace" "the visibility timeout did not go back to 30"
proof "the plan read 45 from the cloud. The value lives there, and was put back to 30."

step "3. two plans approved against the same queue"
explain \
  "Checkout a wants 60 seconds and checkout b wants 90. Each saves its" \
  "plan with -out, and each plan says it changes the timeout from 30." \
  "Both are what a reviewer approves."
cmd "choudoufu plan -out=approved.tfplan   # in a, with 60 ; in b, with 90"
write_config "$SMOKE_WORK/a" 60
write_config "$SMOKE_WORK/b" 90
PLAN_A="$(cd "$SMOKE_WORK/a" && "$RUN_BIN" plan -input=false -no-color -out=approved.tfplan 2>&1)" || fail "echorace" "checkout a's plan -out failed: $PLAN_A"
PLAN_B="$(cd "$SMOKE_WORK/b" && "$RUN_BIN" plan -input=false -no-color -out=approved.tfplan 2>&1)" || fail "echorace" "checkout b's plan -out failed: $PLAN_B"
grep -qE 'visibility_timeout_seconds += 30 -> 60' <<< "$PLAN_A" || fail "echorace" "checkout a's saved plan is not 30 -> 60: $PLAN_A"
grep -qE 'visibility_timeout_seconds += 30 -> 90' <<< "$PLAN_B" || fail "echorace" "checkout b's saved plan is not 30 -> 90: $PLAN_B"
grep -q "1 to change" <<< "$PLAN_A" || fail "echorace" "checkout a's saved plan is not one change: $PLAN_A"
grep -q "1 to change" <<< "$PLAN_B" || fail "echorace" "checkout b's saved plan is not one change: $PLAN_B"
{ echo "a: $(grep -E 'visibility_timeout_seconds' <<< "$PLAN_A" | head -1 | tr -s ' ')"; echo "b: $(grep -E 'visibility_timeout_seconds' <<< "$PLAN_B" | head -1 | tr -s ' ')"; } | evidence
proof "two approved plan files, both changing the same attribute from the same value."

step "4. a's plan applies"
cmd "choudoufu apply approved.tfplan   # in checkout a"
APPLY_A="$(cd "$SMOKE_WORK/a" && "$RUN_BIN" apply -input=false -no-color approved.tfplan 2>&1)" || fail "echorace" "checkout a's approved apply failed: $APPLY_A"
grep -q "Apply complete!" <<< "$APPLY_A" || fail "echorace" "checkout a's approved apply did not complete: $APPLY_A"
[ "$(visibility)" = "60" ] || fail "echorace" "after a's apply the visibility timeout is $(visibility), want 60"
grep -E 'Apply complete!' <<< "$APPLY_A" | evidence
proof "the cloud holds 60. b's plan was approved against 30, which is gone."

step "5. b's plan meets a queue that moved"
explain \
  "b's apply re-reads the queue and plans 60 -> 90. Its approved plan said" \
  "30 -> 90. Same resource, same action, same live queue, same value" \
  "written; the value it is changed FROM moved. That has to refuse, and" \
  "name the before side of the attribute."
cmd "choudoufu apply approved.tfplan   # in checkout b"
CODE=0
APPLY_B="$(cd "$SMOKE_WORK/b" && "$RUN_BIN" apply -input=false -no-color approved.tfplan 2>&1)" || CODE=$?
NOW="$(visibility)"
if [ "${BREAK:-0}" = "1" ]; then
  if [ "$CODE" = "0" ] && [ "$NOW" = "90" ]; then
    echo "b's apply exited 0; the queue's visibility timeout is now $NOW, and a's 60 is gone with no word to either run" | evidence
    proof "caught - with the before-values not compared, b's plan, approved against 30, applied over a's 60 and reported success. That is the silent overwrite."
    ( cd "$SMOKE_WORK/b" && "$RUN_BIN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
    exit 0
  fi
  fail "echorace" "BREAK: the binary with no before-values comparison did not overwrite (exit $CODE, visibility $NOW), so the break did not take and this control proves nothing: $APPLY_B"
fi
[ "$CODE" = "3" ] \
  || fail "echorace" "b's apply exited $CODE, want 3 - a plan approved against a value another apply replaced must refuse with its own status: $APPLY_B"
REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$APPLY_B")"
[ -n "$REFUSAL" ] || fail "echorace" "b's apply exited 3 without the named refusal: $APPLY_B"
R_FLAT="$(flat <<< "$REFUSAL")"
grep -q "disagree about the values it writes" <<< "$R_FLAT" \
  || fail "echorace" "the refusal does not say the values moved: $REFUSAL"
grep -q "before.visibility_timeout_seconds" <<< "$REFUSAL" \
  || fail "echorace" "the refusal does not name the before side of the attribute that moved: $REFUSAL"
if grep -q "after.visibility_timeout_seconds" <<< "$REFUSAL"; then
  fail "echorace" "the refusal names the after side too, but both plans write 90; the comparison is not telling the two sides apart: $REFUSAL"
fi
if grep -q "Apply complete!" <<< "$APPLY_B"; then
  fail "echorace" "b's apply ran anyway after refusing: $APPLY_B"
fi
[ "$NOW" = "60" ] || fail "echorace" "b's apply was refused but the queue's visibility timeout is $NOW, want a's 60"
head -12 <<< "$REFUSAL" | evidence
echo "exit status: $CODE; VisibilityTimeout=$NOW" | evidence
proof "refused by name, exit 3, naming before.visibility_timeout_seconds. The cloud still holds a's 60."

step "6. b's recovery is an ordinary re-plan"
explain \
  "No unlock and no repair verb. b plans again against the queue as it" \
  "is, a reviewer sees 60 -> 90, and that file applies."
cmd "choudoufu plan -out=approved.tfplan && choudoufu apply approved.tfplan   # in checkout b"
REPLAN="$(cd "$SMOKE_WORK/b" && "$RUN_BIN" plan -input=false -no-color -out=approved.tfplan 2>&1)" || fail "echorace" "b's re-plan failed: $REPLAN"
grep -qE 'visibility_timeout_seconds += 60 -> 90' <<< "$REPLAN" || fail "echorace" "b's re-plan is not 60 -> 90: $REPLAN"
REAPPLY="$(cd "$SMOKE_WORK/b" && "$RUN_BIN" apply -input=false -no-color approved.tfplan 2>&1)" || fail "echorace" "b's re-approved apply failed: $REAPPLY"
[ "$(visibility)" = "90" ] || fail "echorace" "after b's re-approved apply the visibility timeout is $(visibility), want 90"
grep -E 'visibility_timeout_seconds' <<< "$REPLAN" | head -1 | evidence
proof "the queue holds 90, from a plan that was approved against 60."

step "7. teardown"
cmd "choudoufu apply -destroy -auto-approve"
D_OUT="$(cd "$SMOKE_WORK/b" && "$RUN_BIN" apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "echorace" "teardown failed: $D_OUT"
destroyed_exactly echorace 1 "$D_OUT"
proof "gone."

echo "  What you watched: two plans approved against one queue's 30-second"
echo "  timeout. The first applied; the second re-read the queue, found 60"
echo "  where its approval said 30, and was refused by name with the cloud"
echo "  left on the first apply's value. Its recovery was a re-plan."
