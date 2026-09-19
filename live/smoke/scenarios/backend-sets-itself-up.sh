# backend-sets-itself-up
# CLAIM 4 - The backend is a bucket with no lock table and no lock: nothing is held, so nothing gets stuck (REAL AWS, maintainer-run). ~6 min.
#
# The slug is older than the headline and stays, because the claim's URL is
# what earlier evidence links to. GitHub issue #1349.
#
# Real AWS for now, and only because of step 3: it runs the shipped
# `just up`, and the pinned emulator's CloudFormation reports CREATE_COMPLETE
# while applying none of an S3 bucket's properties, so the bucket it makes is
# refused by the bucket contract (claim 29). Nothing else here needs an
# account. When the emulator applies those properties, step 0's refusal and
# real_aws in claims.json are what change.

SMOKE_WORK="$SMOKE_WORKROOT/autosetup"
mkdir -p "$SMOKE_WORK/a" "$SMOKE_WORK/b"; export SMOKE_WORK
PROJECT="$ROOT/examples/record-store-bucket"
# shellcheck source=../bucket-iam.sh
. "$SMOKE_DIR/bucket-iam.sh"
cat > "$SMOKE_WORK/a/main.tf" <<'TFEOF'
terraform {
  live {
    estate = "smoke-auto-local"
  }
}

resource "terraform_data" "effect" {
  input = "v1"
}
TFEOF

step "the claim"
explain \
  "Stock's remote-backend day one is: create a bucket, turn on" \
  "versioning, create a lock table, write IAM for both. Three of those" \
  "four are still here. A cloud record store is a bucket, it wants" \
  "versioning, and it wants IAM. The one that is gone is the lock table," \
  "and with it the lock: every write is a single conditional request" \
  "that succeeds or fails atomically and leaves nothing behind. Nothing" \
  "is held across a run, so a run that dies cannot strand the next one," \
  "and there is no force-unlock to reach for at the worst moment." \
  "" \
  "This is a swap and not a subtraction. The bucket also wants a" \
  "lifecycle rule and a public-access block, which stock's list never" \
  "mentioned, so the list is no shorter. What changes is the kind of" \
  "thing that can go wrong: a lock table is in the path of every apply" \
  "and can strand one, and a lifecycle rule and a public-access block" \
  "are set once and are in the path of none." \
  "" \
  "The part that does need no setup is the local store, which steps 1" \
  "and 2 measure. And every store, local or bucket, proves itself before" \
  "a plan trusts it: at first use it writes a sentinel and reads it back" \
  "through the same List call plans use, so a store that cannot answer" \
  "refuses loudly instead of impersonating an empty estate."

step "0. real AWS"
[ "${SMOKE_REAL_AWS:-0}" = "1" ] \
  || fail "auto" "this scenario runs against real AWS: step 3 deploys a CloudFormation stack (one S3 bucket) in the account your credentials name, and removes it. It is maintainer-run and never starts by itself: set SMOKE_REAL_AWS=1 to run it."
for bin in jq just node npm; do command -v "$bin" >/dev/null 2>&1 || fail "auto" "$bin is not installed; the runnable bucket project needs it"; done
real_aws_begin auto
BUCKET="chdf-smoke-auto-$SUFFIX"
[ -d "$PROJECT/node_modules" ] || ( cd "$PROJECT" && npm ci >/dev/null 2>&1 ) || fail "auto" "npm ci failed in $PROJECT"
STACK_UP=0
# Every step prints its own line and the next one runs whatever it said:
# under smoke.sh's `set -euo pipefail` one failed call used to end the trap
# and skip the rest of it silently (#1378). STACK_UP is set BEFORE `just
# up`, so a deploy that fails or is interrupted half way is still torn
# down, which is why each step here tolerates a thing that never appeared.
auto_teardown() {
  set +e
  set +u
  if [ "$STACK_UP" = "1" ]; then
    # `just down` refuses a bucket that holds record versions (step 5 shows
    # it). Emptying it is the deliberate act it asks for.
    if aws s3api head-bucket --bucket "$BUCKET" >/dev/null 2>&1; then
      empty_bucket "$BUCKET"
    else
      echo "  no bucket $BUCKET to empty"
    fi
    if aws cloudformation describe-stacks --stack-name "$BUCKET" >/dev/null 2>&1; then
      aws cloudformation delete-stack --stack-name "$BUCKET" >/dev/null 2>&1 \
        || echo "  COULD NOT ASK for the deletion of stack $BUCKET - remove it by hand" >&2
      aws cloudformation wait stack-delete-complete --stack-name "$BUCKET" >/dev/null 2>&1 \
        && echo "  removed stack and bucket $BUCKET" || echo "  COULD NOT REMOVE stack $BUCKET - remove it by hand" >&2
    else
      echo "  no stack $BUCKET to remove"
    fi
  fi
  # Nothing here registers a role or a bucket today, and this is what makes
  # it safe for one to be added later.
  real_aws_teardown
}
trap 'set +e; set +u; auto_teardown; cleanup' EXIT

step "1. no store declared - the local one appears unbidden"
explain \
  "Copy A declares a live block and one record-backed resource, with" \
  "NOTHING about storage. The way stock implies a local state file, a live block" \
  "implies a local record store: a .tofu-records directory beside the" \
  "module, created at first use, sentinel first."
cmd "choudoufu apply -auto-approve   # no record_store anywhere in the config"
( cd "$SMOKE_WORK/a" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "auto" "init failed"
A_OUT="$(cd "$SMOKE_WORK/a" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "auto" "apply failed: $A_OUT"
grep -qE 'Resources: 1 added' <<< "$A_OUT" || fail "auto" "the record-backed resource did not apply: $A_OUT"
[ -d "$SMOKE_WORK/a/.tofu-records" ] \
  || fail "auto" "no .tofu-records directory appeared - the implied local store did not set itself up"
find "$SMOKE_WORK/a/.tofu-records" -name '.store-sentinel' | head -1 | sed "s|$SMOKE_WORK/a/||" | evidence
[ -n "$(find "$SMOKE_WORK/a/.tofu-records" -name '.store-sentinel')" ] \
  || fail "auto" "the store exists but never provisioned its sentinel"
proof "a directory nobody configured, holding a sentinel the store wrote to prove its own read path, plus the resource's record. Zero setup steps."

step "2. it works: the effect survives between runs"
cmd "choudoufu plan"
P_A="$(cd "$SMOKE_WORK/a" && chdf plan -input=false -no-color 2>&1)" || fail "auto" "replan failed: $P_A"
grep -E 'No changes\.' <<< "$P_A" | head -1 | evidence
grep -q "No changes." <<< "$P_A" || fail "auto" "the record-backed resource did not survive the replan: $P_A"
proof "the store is not scaffolding - the resource's identity round-tripped through it."

step "3. the cloud store is a bucket, stood up once with one command"
explain \
  "Not nothing: a bucket, with the three settings choudoufu asserts" \
  "about it. The repository ships the project that makes one, and" \
  "'just verify' asks the choudoufu binary whether the result is" \
  "correct. What is NOT in this step is the other half of stock's day" \
  "one: no lock table is created, and nothing in the configuration" \
  "names one."
cmd "just up $BUCKET   # examples/record-store-bucket"
# Before the deploy, never after it: a `just up` that creates the stack and
# then fails, or is interrupted, leaves one behind, and a flag set on the
# line after would still read 0 (#1378).
STACK_UP=1
UP_OUT="$(cd "$PROJECT" && RECORD_NONCURRENT_DAYS=7 just up "$BUCKET" 2>&1)" || fail "auto" "just up failed: $UP_OUT"
cmd "just verify $BUCKET"
V_OUT="$(cd "$PROJECT" && CHOUDOUFU_BIN="$TOFU" just verify "$BUCKET" 2>&1)" || fail "auto" "just verify says the bucket it just made is not correct: $V_OUT"
grep -E ' OK |: correct' <<< "$V_OUT" | evidence
grep -q "bucket $BUCKET: correct" <<< "$V_OUT" || fail "auto" "verify did not report the bucket correct: $V_OUT"
# "No lock table is created" was printed and never asked (#1379). The stack's
# own resource list is where a lock table would have to be, and there are only
# two resource types in the answer: the bucket, and with a key its policy.
STACK_TYPES="$(aws cloudformation list-stack-resources --stack-name "$BUCKET" --query 'StackResourceSummaries[].ResourceType' --output text | tr '\t' '\n' | grep -v '^$' | sort -u)"
[ -n "$STACK_TYPES" ] \
  || fail "auto" "the stack $BUCKET lists no resources at all, so 'no lock table' below would be a claim about an empty answer"
UNEXPECTED="$(grep -vxE 'AWS::S3::Bucket|AWS::S3::BucketPolicy' <<< "$STACK_TYPES" || true)"
[ -z "$UNEXPECTED" ] \
  || fail "auto" "the stack makes resources that are not the bucket and its policy: $(echo $UNEXPECTED). Stock's day one made a lock table here; this project must not."
grep -q 'AWS::DynamoDB::Table' <<< "$STACK_TYPES" \
  && fail "auto" "the stack creates a DynamoDB table, which is the lock table this claim says is gone"
echo "$STACK_TYPES" | sed 's/^/stack resource type: /' | evidence
write_b() { # <sleep-seconds for the slow resource, or 0 for none>
  cat > "$SMOKE_WORK/b/main.tf" <<TFEOF
terraform {
  live {
    estate = "smoke-auto-bucket"

    record_store "s3" {
      bucket = "$BUCKET"
      region = "$AWS_REGION"
    }
  }
}

resource "terraform_data" "effect" {
  input = "v1"
}
TFEOF
  [ "$1" = "0" ] || cat >> "$SMOKE_WORK/b/main.tf" <<TFEOF

resource "terraform_data" "slow" {
  input = "v1"

  provisioner "local-exec" {
    command = "echo \$\$ > '$SMOKE_WORK/slow.pid'; touch '$SMOKE_WORK/slow-started'; exec sleep $1"
  }
}
TFEOF
}
write_b 0
cmd "choudoufu apply -auto-approve   # record_store \"s3\" { bucket = ... } is all the configuration says"
( cd "$SMOKE_WORK/b" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "auto" "copy b init failed"
B_OUT="$(cd "$SMOKE_WORK/b" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "auto" "the bucket-backed apply failed: $B_OUT"
grep -qE 'Resources: 1 added' <<< "$B_OUT" || fail "auto" "the bucket-backed apply: $B_OUT"
SENTINEL="$(aws s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-auto-bucket/ --query 'Contents[].Key' --output text | tr '\t' '\n' | grep 'store-sentinel' | head -1)"
[ -n "$SENTINEL" ] || fail "auto" "no sentinel object exists in the bucket - the declared store never proved itself"
aws s3api get-object --bucket "$BUCKET" --key "$SENTINEL" /dev/stdout 2>/dev/null | head -c 90 | evidence
echo | evidence
proof "one command made the bucket, the binary says it is correct, and on first use the store wrote its sentinel where any S3 tool can read it. A bucket, versioning and IAM, the same as stock. The stack's resources are the two types listed above and nothing else, so there is no lock table."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a store that cannot answer must refuse, never impersonate emptiness"
  explain \
    "What this arm proves, exactly: a record store the run cannot reach" \
    "makes the run REFUSE, by name, proposing nothing. It points S3 at a" \
    "closed port, which any S3 client fails, so it does NOT show that the" \
    "sentinel is what caught it - a store that answered but was empty is" \
    "a different corruption and this arm does not make it (#1379). The" \
    "failure class it is on watch for is #693's: a run that reads an" \
    "unreachable or empty store as an empty estate and plans to re-create" \
    "live resources. Step 4, the headline, has no red arm of its own." \
    "" \
    "Only S3 is cut, via the SDK's service-specific endpoint override, so" \
    "the rest of the run is unchanged."
  cmd "AWS_ENDPOINT_URL_S3=http://localhost:9 choudoufu plan"
  BOUT="$(cd "$SMOKE_WORK/b" && AWS_ENDPOINT_URL_S3=http://localhost:9 AWS_MAX_ATTEMPTS=1 chdf plan -input=false -no-color 2>&1)" && \
    fail "auto" "the plan SUCCEEDED against an unreachable record store: $BOUT"
  grep -q "to add" <<< "$BOUT" && fail "auto" "the run proposed creating resources against a store it could not reach: $BOUT"
  grep -qiE "record.store|sentinel" <<< "$BOUT" \
    || fail "auto" "the run failed but nothing named the record store - an anonymous failure is not the loud refusal the claim promises: $BOUT"
  grep -iE "record.store|sentinel" <<< "$BOUT" | head -1 | evidence
  proof "caught - a store that cannot be reached is refused by name, and nothing is proposed. What made it fail is not shown here: an S3 client cannot reach a closed port either way."
  exit 0
fi

step "4. a run killed in the middle of an apply strands nothing"
explain \
  "The measurement the headline rests on. A second resource takes a" \
  "while to create. The apply is killed with SIGKILL while it is in" \
  "flight - no handler runs, nothing gets to clean up. Under stock a" \
  "run that dies this way leaves its lock behind, and the next run" \
  "stops at 'Error acquiring the state lock' until somebody decides" \
  "force-unlock is safe. Here the very next run must just work."
write_b 120
rm -f "$SMOKE_WORK/slow-started"
cmd "choudoufu apply -auto-approve &   # then: kill -9, mid-apply"
( cd "$SMOKE_WORK/b" && exec "$TOFU" apply -auto-approve -input=false -no-color >"$SMOKE_WORK/killed.out" 2>&1 ) &
APPLY_PID=$!
for i in $(seq 1 90); do [ -f "$SMOKE_WORK/slow-started" ] && break; sleep 1; done
[ -f "$SMOKE_WORK/slow-started" ] || { kill -9 "$APPLY_PID" 2>/dev/null; fail "auto" "the slow resource never started creating, so there was no apply in flight to kill: $(cat "$SMOKE_WORK/killed.out")"; }
kill -0 "$APPLY_PID" 2>/dev/null || fail "auto" "the apply had already exited before it could be killed: $(cat "$SMOKE_WORK/killed.out")"
kill -9 "$APPLY_PID"; wait "$APPLY_PID" 2>/dev/null && fail "auto" "the killed apply exited 0"
# SIGKILL orphans the provisioner's own process. It is this scenario's to end.
kill "$(cat "$SMOKE_WORK/slow.pid" 2>/dev/null)" 2>/dev/null || true
grep -q "Apply complete" "$SMOKE_WORK/killed.out" && fail "auto" "the apply completed; it was not killed in flight"
echo "killed with SIGKILL while terraform_data.slow was creating" | evidence
write_b 1
cmd "choudoufu apply -auto-approve   # the very next run, nothing done in between"
N_OUT="$(cd "$SMOKE_WORK/b" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "auto" "the run after the killed one failed: $N_OUT"
grep -qi "lock" <<< "$N_OUT" && fail "auto" "the run after the killed one mentions a lock: $N_OUT"
grep -E 'Resources: ' <<< "$N_OUT" | head -1 | evidence
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$N_OUT" || fail "auto" "the run after the killed one did not simply finish the work: $N_OUT"
KEYS="$(aws s3api list-objects-v2 --bucket "$BUCKET" --query 'Contents[].Key' --output text | tr '\t' '\n' | grep -v '^$' || true)"
echo "$KEYS" | evidence
grep -i "lock" <<< "$KEYS" && fail "auto" "an object in the bucket is named like a lock"
# "Every object in the bucket is a sentinel, a hint and the records" was
# printed and not asked (#1379). Every key is matched against the shapes this
# backend writes, and anything else is named. A grep for "lock" only catches
# an object that says what it is; a lock under another name would not.
[ -n "$KEYS" ] || fail "auto" "the bucket is empty after an apply, so there is nothing here to account for"
KNOWN_SHAPES='^tofu-records/[^/]+/\.store-sentinel$|^tofu-records/[^/]+/[^/]+/.+$|^tofu-hints/[^/]+/guided$|^tofu-outputs/[^/]+/[^/]+$'
STRANGERS="$(grep -vE "$KNOWN_SHAPES" <<< "$KEYS" || true)"
[ -z "$STRANGERS" ] \
  || fail "auto" "the bucket holds object(s) of no shape this backend writes: $(echo $STRANGERS). The shapes are a store sentinel, a record, a guided-discovery hint and a root output; anything else is something nobody has accounted for."
for shape in 'tofu-records/[^/]+/\.store-sentinel$' 'tofu-records/[^/]+/[^/]+/' 'tofu-hints/[^/]+/guided$'; do
  grep -qE "$shape" <<< "$KEYS" \
    || fail "auto" "no object in the bucket matches $shape, so the sentence about what the bucket holds is naming something that is not there: $KEYS"
done
proof "the next run finished the work, first try. Every object in the bucket is listed above and every one of them is a sentinel, a record or a guided-discovery hint, matched key by key. There is no lock object because there is no lock, and so nothing for a dead run to leave held."

step "5. teardown - and this time there IS something to deprovision"
cmd "choudoufu apply -destroy -auto-approve   # in both copies"
( cd "$SMOKE_WORK/a" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "auto" "copy a teardown failed"
D_OUT="$(cd "$SMOKE_WORK/b" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "auto" "copy b teardown failed: $D_OUT"
grep -q "2 destroyed" <<< "$D_OUT" || fail "auto" "copy b's teardown did not destroy both instances: $D_OUT"
cmd "just down $BUCKET"
DN_OUT="$(cd "$PROJECT" && just down "$BUCKET" 2>&1)" && fail "auto" "just down removed a bucket that still held recoverable record versions: $DN_OUT"
grep -q "REFUSING" <<< "$DN_OUT" || fail "auto" "just down failed, but not by refusing: $DN_OUT"
grep -E 'REFUSING|It holds' <<< "$DN_OUT" | head -2 | evidence
proof "both estates gone. The local store leaves a directory. The bucket is a thing somebody stood up and somebody has to take down, and it refuses to go while it still holds the recoverable versions of destroyed records. This scenario empties it on exit, deliberately."

echo "  What you watched: a store nobody configured appear with its sentinel"
echo "  already written; a cloud store that is a bucket, stood up with one"
echo "  command and checked by the binary; an apply killed outright and the"
echo "  next run carrying on as if nothing had happened, because nothing was"
echo "  held; and a teardown that admits there is a bucket to take down."
