# a-bulk-read-is-complete-or-it-fails
# CLAIM 31 - A record read that fails mid-fanout fails the read; a short map never reaches a plan. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/bulkread"
BUCKET="smoke-bulkread-records"
N=12
mkdir -p "$SMOKE_WORK/est"; export SMOKE_WORK
cat > "$SMOKE_WORK/est/main.tf" <<TFEOF
terraform {
  live {
    estate = "smoke-bulkread"

    record_store "s3" {
      bucket = "$BUCKET"
    }

    # One attempt per request, so a failed GET reaches the record store as a
    # failure. With the SDK's default of three, a single 500 is retried away
    # below the code this claim is about and nothing here would be measured.
    retry {
      max_attempts = 1
    }
  }
}

resource "terraform_data" "effect" {
  count = $N
  input = "v\${count.index}"
}
TFEOF

# The proxy (live/smoke/s3proxy.py). It forwards every request to the emulator
# untouched, except that while $SMOKE_WORK/fail holds "<substring> <count>", a
# GetObject whose path contains the substring is answered with a 500. That is
# the only way to fail one GET out of a fan-out from outside the binary: the
# emulator has no fault injection, and nothing in the cloud can be corrupted
# into a 500.

step "the claim"
explain \
  "A plan reads the estate's records in one bulk read: a LIST, then one" \
  "GET per record, eight at a time. What comes back is read as complete -" \
  "a declared resource with no record is something to CREATE, and a" \
  "record with no configuration is something to DESTROY. So the worst" \
  "thing that read can do is succeed with a record missing, and running" \
  "the GETs in parallel is exactly where that would come from: a loop" \
  "returns on its first error for free, a fan-out has to be written to." \
  "This claim fails one GET out of $N and follows it all the way to the" \
  "plan, because the harm is a plan, not a map."

# The binary under test. BREAK=1 swaps in one whose fan-out drops a failed
# GET's key and carries on, which is the defect.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a fan-out that drops a failed key must be caught at the plan"
  explain \
    "The corruption is in the binary: the fan-out swallows a failed call" \
    "instead of failing the read. It is built with go build -overlay, so" \
    "the source tree is never touched, and it needs this checkout and Go."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "bulkread" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "bulkread" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/live/staterecord/bulk.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/bulk.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
old = "\t\t\t\t\tfailOnce.Do(func() {\n\t\t\t\t\t\tfailure = err\n\t\t\t\t\t\tcancel()\n\t\t\t\t\t})\n"
assert src.count(old) == 1, "the break patch no longer matches boundedFanOut"
open(sys.argv[2], "w").write(src.replace(old, "\t\t\t\t\tfailOnce.Do(func() {})\n"))
PYEOF
  [ -s "$SMOKE_WORK/break/bulk.go" ] || fail "bulkread" "the break patch did not apply to $SRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/bulk.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # a failed GET drops its key"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "bulkread" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi
run() { ( cd "$SMOKE_WORK/est" && "$RUN_BIN" "$@" ); }
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }
# max_in_flight reads the proxy's high-water mark of concurrent record GETs.
# The mark is reset by deleting the file, so a plan measured here can never
# read a number an earlier plan left behind.
RECORD_PREFIX="tofu-records/smoke-bulkread/terraform_data/"
arm_counter() { echo "$RECORD_PREFIX" > "$SMOKE_WORK/count"; echo "$RECORD_PREFIX 0.2" > "$SMOKE_WORK/stall"; rm -f "$SMOKE_WORK/inflight-max"; }
max_in_flight() { cat "$SMOKE_WORK/inflight-max" 2>/dev/null || echo 0; }

step "1. $N record-backed resources, applied straight to the emulator"
stack_up
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "bulkread" "could not create the bucket"
awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled >/dev/null
awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >/dev/null
awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null
run init -input=false -no-color >/dev/null 2>&1 || fail "bulkread" "init failed"
cmd "choudoufu apply -auto-approve"
OUT="$(run apply -auto-approve -input=false -no-color 2>&1)" || fail "bulkread" "apply failed: $OUT"
grep -q "Resources: $N added" <<< "$OUT" || fail "bulkread" "expected $N resources: $OUT"
KEYS="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix tofu-records/smoke-bulkread/terraform_data/ --query 'Contents[].Key' --output text | tr '\t' '\n')"
[ "$(wc -l <<< "$KEYS" | tr -d ' ')" = "$N" ] || fail "bulkread" "expected $N record objects, found: $KEYS"
VICTIM="$(sed -n '7p' <<< "$KEYS")"
echo "$N records; the one this scenario will fail: $VICTIM" | evidence
proof "every resource has a record, and one of them is singled out."

step "2. the proxy, a control plan through it with nothing failing, and how wide the fan-out is"
python3 "$SMOKE_DIR/s3proxy.py" "$FLOCI_PORT" "$SMOKE_WORK" 2>"$SMOKE_WORKROOT/logs/bulkread-proxy.err" &
PROXY_PID=$!
trap 'kill $PROXY_PID 2>/dev/null || true; cleanup' EXIT
for _ in $(seq 1 50); do [ -s "$SMOKE_WORK/proxy.port" ] && break; sleep 0.1; done
[ -s "$SMOKE_WORK/proxy.port" ] || fail "bulkread" "the proxy never started"
export AWS_ENDPOINT_URL="http://localhost:$(cat "$SMOKE_WORK/proxy.port")"
# The state cache would answer the plan without asking the store at all.
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
arm_counter
cmd "choudoufu plan   # through the proxy, nothing failing"
C_OUT="$(run plan -input=false -no-color 2>&1)" || fail "bulkread" "the control plan through the proxy failed: $C_OUT"
grep -q "No changes." <<< "$C_OUT" || fail "bulkread" "the control plan through the proxy was not empty, so the proxy itself changes the answer: $C_OUT"
GETS="$(grep -c '^GET /tofu-records/smoke-bulkread/terraform_data/' "$SMOKE_WORK/proxy.log" || true)"
[ "$GETS" -ge "$N" ] || fail "bulkread" "the proxy saw $GETS record GETs, fewer than the $N records: the plan is not reading records through it, and failing one would prove nothing"
# "Eight at a time" was in the claim's own words and nothing measured it
# (#1379). The proxy counts record GETs in flight and keeps the high-water
# mark; with each GET held for 200ms, requests that really are concurrent
# overlap for long enough to be counted. 8 is
# staterecord.DefaultS3GetAllParallelism.
PARALLEL="$(max_in_flight)"
[ "$PARALLEL" -gt 1 ] \
  || fail "bulkread" "the proxy never saw more than $PARALLEL record GET in flight at once: this read is sequential, not a fan-out, and the claim's whole subject is what a fan-out does with a failure"
[ "$PARALLEL" -le 8 ] \
  || fail "bulkread" "the proxy saw $PARALLEL record GETs in flight at once, past the default bound of 8"
echo "record GETs seen by the proxy: $GETS, all 200; most in flight at once: $PARALLEL (the default bound is 8)" | evidence
# The other direction, so the counter is known to be counting and not just
# printing a number that happens to be in range: the same plan with the
# fan-out turned down to one worker must never exceed one in flight.
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
arm_counter
cmd "TOFU_LIVE_RECORD_READ_PARALLELISM=1 choudoufu plan   # the sequential read, for contrast"
S_OUT="$(cd "$SMOKE_WORK/est" && TOFU_LIVE_RECORD_READ_PARALLELISM=1 "$RUN_BIN" plan -input=false -no-color 2>&1)" \
  || fail "bulkread" "the sequential control plan failed: $S_OUT"
grep -q "No changes." <<< "$S_OUT" || fail "bulkread" "the sequential control plan was not empty: $S_OUT"
SEQUENTIAL="$(max_in_flight)"
[ "$SEQUENTIAL" = "1" ] \
  || fail "bulkread" "with TOFU_LIVE_RECORD_READ_PARALLELISM=1 the proxy saw $SEQUENTIAL record GETs in flight at once, want exactly 1; the counter is not measuring the fan-out"
echo "the same plan with TOFU_LIVE_RECORD_READ_PARALLELISM=1: most in flight at once: $SEQUENTIAL" | evidence
rm -f "$SMOKE_WORK/stall" "$SMOKE_WORK/count"
proof "the plan reads every record through the proxy, $PARALLEL of them in flight at once against the default and exactly one with the fan-out turned off, and it is empty. Whatever changes next is the failure's doing."

step "3. one GET fails once, mid-fanout"
explain \
  "The realistic fault: a throttle, a reset connection. One record's GET" \
  "is answered 500, once. The bulk read must not come back short. It may" \
  "fail, and the run may then read its records one at a time instead, but" \
  "the plan that results has to be the true one."
: > "$SMOKE_WORK/proxy.log"
echo "$VICTIM 1" > "$SMOKE_WORK/fail"
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
cmd "choudoufu plan   # $VICTIM answers 500 once"
T_OUT="$(run plan -input=false -no-color 2>&1)" && T_RC=0 || T_RC=$?
grep -c ' 500$' "$SMOKE_WORK/proxy.log" | sed 's/^/requests the proxy failed: /' | evidence
[ "$(grep -c ' 500$' "$SMOKE_WORK/proxy.log")" -ge 1 ] || fail "bulkread" "the proxy failed nothing, so this step measured an ordinary plan"
if grep -qE 'Plan: [1-9][0-9]* to add' <<< "$T_OUT"; then
  if [ "${BREAK:-0}" = "1" ]; then
    grep -E 'Plan:|will be created' <<< "$T_OUT" | head -3 | evidence
    proof "caught - with the failed key dropped, the read came back one record short and the plan proposes CREATING a resource that exists. That plan is the harm."
    exit 0
  fi
  fail "bulkread" "a record read that failed for one key produced a plan that proposes creating that resource: $T_OUT"
fi
[ "${BREAK:-0}" = "1" ] && fail "bulkread" "the binary built to drop a failed key still produced a true plan, so the break did not take and this control proves nothing: $T_OUT"
# Two outcomes are allowed here and both are named, because "no resource was
# proposed for creation" is not the claim: the claim is that the plan is the
# TRUE one. Until #1379 a run that exited 0 was accepted whatever it planned,
# so a plan of twelve changes read exactly like an empty one.
if [ "$T_RC" = "0" ]; then
  grep -q "No changes." <<< "$T_OUT" \
    || fail "bulkread" "the plan exited 0 and is not empty, although nothing in the world changed: a read that came back with something other than what is in the bucket is the failure this claim is about. $T_OUT"
else
  grep -q "$(basename "$VICTIM")" <<< "$(flat <<< "$T_OUT")" \
    || fail "bulkread" "the plan failed without naming the record it could not read: $T_OUT"
fi
grep -E 'No changes\.|Error:' <<< "$T_OUT" | head -1 | evidence
proof "the plan is the true one: empty if the run got its records, and a refusal naming the record if it did not. Nothing was proposed for creation either way."

step "4. the same GET fails every time"
explain \
  "Now the record cannot be read at all. There is no true plan to be had," \
  "so the run must refuse and name the record, never plan around it."
: > "$SMOKE_WORK/proxy.log"
echo "$VICTIM -1" > "$SMOKE_WORK/fail"
rm -f "$SMOKE_WORK/est/.terraform/choudoufu-cache.tfstate"
cmd "choudoufu plan   # $VICTIM answers 500 every time"
P_OUT="$(run plan -input=false -no-color 2>&1)" && fail "bulkread" "the plan SUCCEEDED with a record that cannot be read: $P_OUT"
grep -qE 'Plan: [1-9][0-9]* to add' <<< "$P_OUT" && fail "bulkread" "the run failed but still rendered a plan that creates the unreadable resource: $P_OUT"
grep -q "$(basename "$VICTIM")" <<< "$(flat <<< "$P_OUT")" || fail "bulkread" "the refusal does not name the record it could not read: $P_OUT"
grep -E 'Error:' <<< "$P_OUT" | head -1 | evidence
proof "refused, naming the record. An unreadable record is not an absent one."

step "5. teardown"
rm -f "$SMOKE_WORK/fail"
cmd "choudoufu apply -destroy -auto-approve"
D_OUT="$(run apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "bulkread" "teardown failed: $D_OUT"
destroyed_exactly bulkread "$N" "$D_OUT"
proof "gone."

echo "  What you watched: $N records read through a proxy, one GET failed once"
echo "  and then always, and no plan at any point proposing to create a"
echo "  resource whose record was merely unreadable."
