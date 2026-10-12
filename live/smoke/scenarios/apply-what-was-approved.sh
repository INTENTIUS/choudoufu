# apply-what-was-approved
# CLAIM 15 (aws) - Apply exactly what was approved. ~7 min.
#
# This proof: Apply exactly what was approved: a saved plan crosses the
# approval gate, and the apply that consumes it still reads the live system
# - matching down to the planned values, or refusing by name.
#
# Two halves. Steps 1-7 change one estate behind an approved plan in two
# ways: a marked subnet appears (refused, naming aws_subnet.crashed), then
# the configuration's value moves (refused, naming
# after.retention_in_days). Steps 8-14 were two-saved-plans-one-attribute,
# claim 2's proof until it merged here: two checkouts of one estate approve
# plans against one SQS queue's visibility timeout, and the second apply
# is refused naming before.visibility_timeout_seconds because the value it
# was planned FROM is gone (GitHub issue #1504, ruling 4; #878). That is
# what referees two saved-plan applies on an attribute the provider reads
# back from the cloud, since no conditional record write is checked there
# (internal/live/approval/values.go).
#
# Two plain `apply` runs are last-writer-wins on such an attribute, and
# this scenario does not claim otherwise; site/content/docs/model/
# concurrency.md says so. Two saved-plan applies can also both re-read
# before either writes (#1504's gap 3); steps 11 and 12 apply them one
# after the other, which is the case the before-values check covers.
#
#   BREAK=1          rebuilds choudoufu with the before-values comparison
#                    removed (go build -overlay) and skips steps 1-7; it
#                    passes only when the second saved plan is caught
#                    reporting success while the cloud ends on its value:
#                    the silent overwrite.
#   BREAK_UNMOVED=1  steps 1-4 with the world left unmoved: the approved
#                    plan must then apply, so the refusal is earned by the
#                    drift and not handed to every plan file.

SMOKE_WORK="$SMOKE_WORKROOT/approval"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"
# The race's two checkouts sit beside the estate, outside its root module.
RACE="$SMOKE_WORKROOT/approval-race"
mkdir -p "$RACE/a" "$RACE/b"

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

# The binary under test. BREAK=1 swaps in one whose approval check never
# compares before-values.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - without the before-values check the second saved plan must be caught overwriting"
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

stack_up

export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "CI runs Terraform as: plan on the pull request, a human approves," \
  "apply exactly what was approved. The artifact that crosses that gate" \
  "is the plan file. Under live markers the world stays authoritative -" \
  "an apply always re-reads it and plans against what is there now - so" \
  "the plan file is not an instruction to replay. It is the record of" \
  "what was approved, and the apply compares its own fresh plan against" \
  "it. Same plan, it applies. Different plan, it refuses by name and" \
  "exits 3, which is a pipeline's signal to send the change back to" \
  "review. The same check referees two approved plans that change one" \
  "attribute the provider reads back from the cloud: the second was" \
  "approved against a value the first apply replaced."

# Steps 1-7 are the change under review against the moving world. The
# overlay control is about the race below, so it skips them.
if [ "${BREAK:-0}" != "1" ]; then
step "1. stand the estate up"
cmd "choudoufu init && choudoufu apply -auto-approve"
logged apply-what-was-approved-init "approval" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "approval" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=live-e2e-block" --query 'Vpcs[0].VpcId' --output text)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "approval" "the estate's VPC is not there after the apply"
proof "a live estate, every resource carrying its ownership markers."

step "2. the change under review - plan -out writes the artifact"
explain \
  "A reviewer's change: the log group's retention goes from one day to" \
  "three. The plan is saved with -out, the stock flag, into the stock" \
  "file format. This is what a pipeline attaches to the pull request and" \
  "what a human approves."
cmd "choudoufu plan -out=approved.tfplan"
sed_i "$SMOKE_WORK/logs.tf" 's/retention_in_days = 1/retention_in_days = 3/'
PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color -out=approved.tfplan 2>&1)" \
  || fail "approval" "plan -out failed: $PLAN_OUT"
[ -f "$SMOKE_WORK/approved.tfplan" ] || fail "approval" "plan -out wrote no file: $PLAN_OUT"
grep -E "aws_cloudwatch_log_group.app will be updated|retention_in_days" <<< "$PLAN_OUT" | head -3 | evidence
grep -q "aws_cloudwatch_log_group.app" <<< "$PLAN_OUT" \
  || fail "approval" "the saved plan is not about the change under review: $PLAN_OUT"
proof "$(wc -c < "$SMOKE_WORK/approved.tfplan" | tr -d ' ') bytes of stock-format plan file, approved and ready to cross the gate."

step "3. the world moves while the approval waits"
explain \
  "Hours pass between the approval and the apply, and something else" \
  "touches the account: a subnet appears carrying this estate's markers" \
  "for an address the configuration does not declare. The next plan must" \
  "propose destroying it - which is a change nobody approved."
cmd "aws ec2 create-subnet ... + create-tags tofu-estate/tofu-address=aws_subnet.crashed"
if [ "${BREAK_UNMOVED:-0}" = "1" ]; then
  explain \
    "BREAK_UNMOVED control: the world is deliberately left UNMOVED. The refusal" \
    "below has to be conditional on the drift, not something the" \
    "comparison hands out to every plan file it is given - a check that" \
    "always fires is not a check. So this run must APPLY, and the" \
    "scenario fails if it refuses."
  echo "nothing changed out of band" | evidence
else
  CRASHED="$(awsl ec2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.0.99.0/24 --query 'Subnet.SubnetId' --output text)"
  [ -n "$CRASHED" ] || fail "approval" "could not create the out-of-band subnet"
  awsl ec2 create-tags --resources "$CRASHED" \
    --tags "Key=tofu-estate,Value=live-e2e-block" "Key=tofu-address,Value=aws_subnet.crashed" \
    || fail "approval" "could not mark the out-of-band subnet"
  echo "created $CRASHED, marked as aws_subnet.crashed - after the approval, before the apply" | evidence
fi
proof "the gap between review and apply, with something in it."

step "4. apply the approved plan"
explain \
  "The apply reads the live system and plans against what it finds, the" \
  "way every live-markers run does. It does not replay the file. It" \
  "compares: same resources, same actions, same live objects, same" \
  "planned values, or it refuses before anything changes."
cmd "choudoufu apply approved.tfplan"
CODE=0
GATE_OUT="$(cd "$SMOKE_WORK" && chdf apply -input=false -no-color approved.tfplan 2>&1)" || CODE=$?
if [ "${BREAK_UNMOVED:-0}" = "1" ]; then
  if [ "$CODE" != "0" ]; then
    fail "approval" "BREAK_UNMOVED: the apply refused (exit $CODE) with the world unmoved - the comparison refuses unconditionally, so the refusal it produces is not evidence of anything: $GATE_OUT"
  fi
  grep -E 'Apply complete!' <<< "$GATE_OUT" | evidence
  proof "caught - with nothing moved between review and apply the same file applied, so the refusal this scenario asserts is earned by the drift and not handed out to every plan file."
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi
[ "$CODE" = "3" ] \
  || fail "approval" "the apply exited $CODE, want 3 - a plan file whose approval no longer covers the run must refuse with its own status: $GATE_OUT"
grep -q "The approved plan no longer matches the live system" <<< "$GATE_OUT" \
  || fail "approval" "the apply stopped, but not with the named refusal: $GATE_OUT"
# Everything from the refusal's own summary line onward. The plan above it
# also names the subnet, so asserting over the whole output would pass on a
# refusal that named nothing at all.
REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$GATE_OUT")"
grep -q "aws_subnet.crashed" <<< "$REFUSAL" \
  || fail "approval" "the refusal does not name the change nobody approved: $REFUSAL"
grep -q "Exit status 3" <<< "$REFUSAL" \
  || fail "approval" "the refusal does not tell a pipeline what its exit status means: $REFUSAL"
head -12 <<< "$REFUSAL" | evidence
echo "exit status: $CODE" | evidence
if grep -q "Apply complete!" <<< "$GATE_OUT"; then
  fail "approval" "the apply ran anyway after refusing: $GATE_OUT"
fi
proof "refused by name, exit 3, nothing applied - and the row it names is exactly the change that appeared after the approval."

step "5. the same change, a different value"
explain \
  "The subtler failure, and the one a comparison over resource names" \
  "alone would wave through: the change set is IDENTICAL - the same" \
  "resource, the same update, the same live log group - and what it" \
  "writes moved. Someone edited the configuration after the approval and" \
  "asked for fourteen days instead of three. The apply has to refuse" \
  "that too, and name the attribute."
cmd "edit retention_in_days 3 -> 14 ; choudoufu apply approved.tfplan"
awsl ec2 delete-subnet --subnet-id "$CRASHED" >/dev/null 2>&1 || fail "approval" "could not remove the out-of-band subnet"
sed_i "$SMOKE_WORK/logs.tf" 's/retention_in_days = 3/retention_in_days = 14/'
VCODE=0
VALUE_OUT="$(cd "$SMOKE_WORK" && chdf apply -input=false -no-color approved.tfplan 2>&1)" || VCODE=$?
[ "$VCODE" = "3" ] \
  || fail "approval" "the apply exited $VCODE, want 3 - the same change writing a different value must refuse like any other difference: $VALUE_OUT"
VALUE_REFUSAL="$(sed -n '/The approved plan no longer matches the live system/,$p' <<< "$VALUE_OUT")"
# Collapsed to one line first: the diagnostic printer folds at its width,
# and this assertion is about the sentence, not about where the fold landed.
VALUE_FLAT="$(tr '\n' ' ' <<< "$VALUE_REFUSAL" | tr -s ' ')"
grep -q "disagree about the values it writes" <<< "$VALUE_FLAT" \
  || fail "approval" "the refusal does not say the values moved: $VALUE_REFUSAL"
grep -q "after.retention_in_days" <<< "$VALUE_REFUSAL" \
  || fail "approval" "the refusal does not name the attribute that moved: $VALUE_REFUSAL"
head -14 <<< "$VALUE_REFUSAL" | evidence
echo "exit status: $VCODE" | evidence
sed_i "$SMOKE_WORK/logs.tf" 's/retention_in_days = 14/retention_in_days = 3/'
proof "refused again, exit 3, and it names after.retention_in_days - the values are part of what was approved, not just the resource names."

step "6. re-plan, re-approve, apply"
explain \
  "The way forward the refusal names: plan again against the world as it" \
  "is now, review THAT, and apply the artifact it produced. The same two" \
  "commands, and this time they agree."
cmd "choudoufu plan -out=approved.tfplan && choudoufu apply approved.tfplan"
( cd "$SMOKE_WORK" && chdf plan -input=false -no-color -out=approved.tfplan >/dev/null 2>&1 ) \
  || fail "approval" "the re-plan failed"
SECOND="$(cd "$SMOKE_WORK" && chdf apply -input=false -no-color approved.tfplan 2>&1)" \
  || fail "approval" "the re-approved apply failed: $SECOND"
grep -E 'Apply complete!' <<< "$SECOND" | evidence
grep -q "Apply complete!" <<< "$SECOND" || fail "approval" "the re-approved apply did not complete: $SECOND"
RETENTION="$(awsl logs describe-log-groups --log-group-name-prefix "/live-e2e-block/app" --query 'logGroups[0].retentionInDays' --output text)"
[ "$RETENTION" = "3" ] \
  || fail "approval" "the approved change did not land: retention is $RETENTION, want 3"
proof "retention is $RETENTION days, exactly what was reviewed. The gate held twice and then let the right thing through."

step "7. teardown"
cmd "choudoufu apply -destroy -auto-approve"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "approval" "teardown failed"
proof "estate gone."

fi

step "8. one estate, one queue, two checkouts of it"
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "echorace" "could not create the record bucket"
awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled >/dev/null
awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >/dev/null
awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null
write_config "$RACE/a" 30
write_config "$RACE/b" 30
logged apply-what-was-approved-race-a-init "echorace" "init failed in a" -- in_dir "$RACE/a" "$RUN_BIN" init -input=false -no-color
logged apply-what-was-approved-race-b-init "echorace" "init failed in b" -- in_dir "$RACE/b" "$RUN_BIN" init -input=false -no-color
cmd "choudoufu apply -auto-approve   # in checkout a"
OUT="$(cd "$RACE/a" && "$RUN_BIN" apply -auto-approve -input=false -no-color 2>&1)" || fail "echorace" "the seeding apply failed: $OUT"
[ "$(visibility)" = "30" ] || fail "echorace" "the queue's visibility timeout is $(visibility) after the seeding apply, want 30"
echo "VisibilityTimeout=$(visibility)" | evidence
proof "one queue, its visibility timeout 30 seconds in the cloud."

step "9. the attribute is echoed"
explain \
  "The race below is only about the cloud if the provider reads this" \
  "attribute back. So the cloud is changed behind both checkouts, and" \
  "checkout b's plan must see it: a value it can only have read from" \
  "GetQueueAttributes, since nothing it holds was told."
cmd "aws sqs set-queue-attributes VisibilityTimeout=45 ; choudoufu plan   # in checkout b"
set_visibility 45 || fail "echorace" "could not change the visibility timeout out of band"
ECHO_PLAN="$(cd "$RACE/b" && "$RUN_BIN" plan -input=false -no-color 2>&1)" || fail "echorace" "checkout b's plan failed: $ECHO_PLAN"
grep -E 'visibility_timeout_seconds' <<< "$ECHO_PLAN" | head -2 | evidence
grep -qE 'visibility_timeout_seconds += 45 -> 30' <<< "$ECHO_PLAN" \
  || fail "echorace" "checkout b's plan did not read the out-of-band 45 back from the cloud, so this attribute is not echoed and the race below would not be about the cloud: $ECHO_PLAN"
set_visibility 30 || fail "echorace" "could not put the visibility timeout back"
[ "$(visibility)" = "30" ] || fail "echorace" "the visibility timeout did not go back to 30"
proof "the plan read 45 from the cloud. The value lives there, and was put back to 30."

step "10. two plans approved against the same queue"
explain \
  "Checkout a wants 60 seconds and checkout b wants 90. Each saves its" \
  "plan with -out, and each plan says it changes the timeout from 30." \
  "Both are what a reviewer approves."
cmd "choudoufu plan -out=approved.tfplan   # in a, with 60 ; in b, with 90"
write_config "$RACE/a" 60
write_config "$RACE/b" 90
PLAN_A="$(cd "$RACE/a" && "$RUN_BIN" plan -input=false -no-color -out=approved.tfplan 2>&1)" || fail "echorace" "checkout a's plan -out failed: $PLAN_A"
PLAN_B="$(cd "$RACE/b" && "$RUN_BIN" plan -input=false -no-color -out=approved.tfplan 2>&1)" || fail "echorace" "checkout b's plan -out failed: $PLAN_B"
grep -qE 'visibility_timeout_seconds += 30 -> 60' <<< "$PLAN_A" || fail "echorace" "checkout a's saved plan is not 30 -> 60: $PLAN_A"
grep -qE 'visibility_timeout_seconds += 30 -> 90' <<< "$PLAN_B" || fail "echorace" "checkout b's saved plan is not 30 -> 90: $PLAN_B"
grep -q "1 to change" <<< "$PLAN_A" || fail "echorace" "checkout a's saved plan is not one change: $PLAN_A"
grep -q "1 to change" <<< "$PLAN_B" || fail "echorace" "checkout b's saved plan is not one change: $PLAN_B"
{ echo "a: $(grep -E 'visibility_timeout_seconds' <<< "$PLAN_A" | head -1 | tr -s ' ')"; echo "b: $(grep -E 'visibility_timeout_seconds' <<< "$PLAN_B" | head -1 | tr -s ' ')"; } | evidence
proof "two approved plan files, both changing the same attribute from the same value."

step "11. a's plan applies"
cmd "choudoufu apply approved.tfplan   # in checkout a"
APPLY_A="$(cd "$RACE/a" && "$RUN_BIN" apply -input=false -no-color approved.tfplan 2>&1)" || fail "echorace" "checkout a's approved apply failed: $APPLY_A"
grep -q "Apply complete!" <<< "$APPLY_A" || fail "echorace" "checkout a's approved apply did not complete: $APPLY_A"
[ "$(visibility)" = "60" ] || fail "echorace" "after a's apply the visibility timeout is $(visibility), want 60"
grep -E 'Apply complete!' <<< "$APPLY_A" | evidence
proof "the cloud holds 60. b's plan was approved against 30, which is gone."

step "12. b's plan meets a queue that moved"
explain \
  "b's apply re-reads the queue and plans 60 -> 90. Its approved plan said" \
  "30 -> 90. Same resource, same action, same live queue, same value" \
  "written; the value it is changed FROM moved. That has to refuse, and" \
  "name the before side of the attribute."
cmd "choudoufu apply approved.tfplan   # in checkout b"
CODE=0
APPLY_B="$(cd "$RACE/b" && "$RUN_BIN" apply -input=false -no-color approved.tfplan 2>&1)" || CODE=$?
NOW="$(visibility)"
if [ "${BREAK:-0}" = "1" ]; then
  if [ "$CODE" = "0" ] && [ "$NOW" = "90" ]; then
    echo "b's apply exited 0; the queue's visibility timeout is now $NOW, and a's 60 is gone with no word to either run" | evidence
    proof "caught - with the before-values not compared, b's plan, approved against 30, applied over a's 60 and reported success. That is the silent overwrite."
    ( cd "$RACE/b" && "$RUN_BIN" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
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

step "13. b's recovery is an ordinary re-plan"
explain \
  "No unlock and no repair verb. b plans again against the queue as it" \
  "is, a reviewer sees 60 -> 90, and that file applies."
cmd "choudoufu plan -out=approved.tfplan && choudoufu apply approved.tfplan   # in checkout b"
REPLAN="$(cd "$RACE/b" && "$RUN_BIN" plan -input=false -no-color -out=approved.tfplan 2>&1)" || fail "echorace" "b's re-plan failed: $REPLAN"
grep -qE 'visibility_timeout_seconds += 60 -> 90' <<< "$REPLAN" || fail "echorace" "b's re-plan is not 60 -> 90: $REPLAN"
REAPPLY="$(cd "$RACE/b" && "$RUN_BIN" apply -input=false -no-color approved.tfplan 2>&1)" || fail "echorace" "b's re-approved apply failed: $REAPPLY"
[ "$(visibility)" = "90" ] || fail "echorace" "after b's re-approved apply the visibility timeout is $(visibility), want 90"
grep -E 'visibility_timeout_seconds' <<< "$REPLAN" | head -1 | evidence
proof "the queue holds 90, from a plan that was approved against 60."

step "14. teardown"
cmd "choudoufu apply -destroy -auto-approve"
D_OUT="$(cd "$RACE/b" && "$RUN_BIN" apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "echorace" "teardown failed: $D_OUT"
destroyed_exactly echorace 1 "$D_OUT"
proof "gone."

echo "  What you watched: a plan saved to a file, approved, and then"
echo "  applied against a world that had moved underneath it - refused by"
echo "  name with its own exit status, never replayed. Then refused again"
echo "  for a change that kept its name and its action and moved the value"
echo "  it writes, then applied once re-approved. Then two plans approved"
echo "  against one queue's 30-second timeout: the first applied, the second"
echo "  found 60 where its approval said 30 and was refused by name with the"
echo "  cloud left on the first apply's value. Its recovery was a re-plan."
