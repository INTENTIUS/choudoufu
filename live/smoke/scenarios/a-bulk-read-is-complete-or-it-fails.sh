# a-bulk-read-is-complete-or-it-fails
# CLAIM 31 (aws) - A record read that fails mid-fanout fails the read; a short map never reaches a plan. ~2 min.

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
#
# With a third field, "<substring> <count> 404", the answer is S3's own 404
# NoSuchKey instead: a read that succeeds and says no record is there, while
# the listing, which is never failed, goes on naming the key. Step 5.

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
#
# The corruption takes TWO edits, not one, and it did not always. Until
# GitHub issue #1355 the fan-out's own swallowed failure was enough on its
# own: a key with no record fetched for it was simply left out of the map
# that was built at the end, so swallowing the failure produced the short
# map directly. #1355 made a key with no record fetched for it a refusal in
# its own right, so with only the first edit the broken binary still fails
# the read - for the second reason rather than the first - and the plan it
# produces is true. A control that no longer reaches the thing it controls
# for is not a control, so the second edit puts the omission back.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a fan-out that drops a failed key must be caught at the plan"
  explain \
    "The corruption is in the binary, in two edits that together are the" \
    "bulk read as it stood before issue #1355: the fan-out swallows a" \
    "failed call instead of failing the read, AND the map it builds at the" \
    "end leaves out every key it never fetched a record for, rather than" \
    "refusing over them. Either edit alone leaves a binary that still" \
    "fails the read, which would test nothing. It is built with" \
    "go build -overlay, so the source tree is never touched, and it needs" \
    "this checkout and Go."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "bulkread" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "bulkread" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/live/staterecord/bulk.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/bulk.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()

# Edit 1: the fan-out swallows the failure instead of recording it.
swallow = "\t\t\t\t\tfailOnce.Do(func() {\n\t\t\t\t\t\tfailure = err\n\t\t\t\t\t\tcancel()\n\t\t\t\t\t})\n"
assert src.count(swallow) == 1, "the break patch no longer matches boundedFanOut"
src = src.replace(swallow, "\t\t\t\t\tfailOnce.Do(func() {})\n")

# Edit 2: the map leaves out the keys nothing was fetched for, instead of
# refusing over them (issue #1355). Without this the swallowed failure above
# is caught here instead, and the broken binary produces a true plan.
refuse = """\tvar vanished []string
\tfor i, rec := range found {
\t\tif rec == nil {
\t\t\tvanished = append(vanished, keys[i])
\t\t}
\t}
\tif len(vanished) > 0 {
"""
start = src.index(refuse)
end = src.index("\treturn out, nil\n}", start) + len("\treturn out, nil\n}")
src = src[:start] + """\tout := make(map[string]Record, len(keys))
\tfor i, rec := range found {
\t\tif rec != nil {
\t\t\tout[keys[i]] = *rec
\t\t}
\t}
\treturn out, nil
}""" + src[end:]
assert "namesVanished(vanished)" not in src.split("func (s *S3Store) GetAll")[1].split("\nfunc ")[0], \
    "the break patch left S3Store.GetAll still refusing over an unfetched key"

open(sys.argv[2], "w").write(src)
PYEOF
  [ -s "$SMOKE_WORK/break/bulk.go" ] || fail "bulkread" "the break patch did not apply to $SRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/bulk.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # a failed GET drops its key"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "bulkread" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi

# The second control, for step 5 only. BREAK_CROSSCHECK=1 swaps in a binary
# whose plan never makes the cross-check step 5 is about. It is a separate
# variable from BREAK because it is a separate corruption of a separate file:
# BREAK=1 tears the bulk read's snapshot, and the commit that gave it its
# second edit measured that the cross-check cannot see a torn snapshot at all
# (the listing and the absence both come out of it, so they agree). The
# cross-check is the backstop for the per-key path, and only a store that
# answers 404 on that path reaches it. Steps 1 to 4 run on the real binary
# either way.
PAIR_BIN="$RUN_BIN"
if [ "${BREAK_CROSSCHECK:-0}" = "1" ]; then
  step "BREAK_CROSSCHECK control - without the plan-time cross-check, a destroy must be caught missing a record"
  explain \
    "The corruption is in the binary: the one call to" \
    "refuseListedButReadAsAbsent in internal/live/projection/build.go is" \
    "removed, so a declared instance whose record reads as absent is planned" \
    "around even while the store's own listing names its key. Built with" \
    "go build -overlay, so the source tree is never touched, and it needs" \
    "this checkout and Go. What it must produce in step 5 is GitHub issue" \
    "#1355's output, manufactured: one destroyed of two, and exit 0."
  [ "${BREAK:-0}" = "1" ] && fail "bulkread" "BREAK=1 and BREAK_CROSSCHECK=1 are two controls over two different files, and BREAK=1 stops at step 3; run them one at a time"
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "bulkread" "BREAK_CROSSCHECK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "bulkread" "BREAK_CROSSCHECK=1 needs Go to build the broken binary"
  XSRC="$ROOT/internal/live/projection/build.go"
  mkdir -p "$SMOKE_WORK/breakx"
  python3 - "$XSRC" "$SMOKE_WORK/breakx/build.go" <<'PYEOF' || fail "bulkread" "the break patch did not apply to $XSRC (the assertion above says which part), so this arm would pass by testing the real binary"
import sys
src = open(sys.argv[1]).read()
call = "\t\t\tb.refuseListedButReadAsAbsent(addr, key)\n"
assert src.count(call) == 1, "the break patch no longer matches discoverOrphanedRecords' one call to refuseListedButReadAsAbsent"
src = src.replace(call, "")
assert "b.refuseListedButReadAsAbsent(" not in src, "the break patch left a call to refuseListedButReadAsAbsent behind"
open(sys.argv[2], "w").write(src)
PYEOF
  [ -s "$SMOKE_WORK/breakx/build.go" ] || fail "bulkread" "the break patch did not apply to $XSRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$XSRC" "$SMOKE_WORK/breakx/build.go" > "$SMOKE_WORK/breakx/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # the plan no longer checks the listing against what it read as absent"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/breakx/overlay.json" -o "$SMOKE_WORK/breakx/choudoufu" ./cmd/choudoufu ) \
    || fail "bulkread" "the broken binary did not build"
  PAIR_BIN="$SMOKE_WORK/breakx/choudoufu"
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

step "5. a record the listing names and the GET does not find"
explain \
  "A different fault from a 500, and not a milder one. A 500 is a read" \
  "that failed. A 404 is a read that SUCCEEDED and said no record is" \
  "there, and a record-backed instance with no record has no prior state:" \
  "a plan proposes creating it, and a destroy, which is built from prior" \
  "state alone, proposes nothing for it and reports success one short." \
  "That is GitHub issue #1355, seen once on real AWS: a two-instance" \
  "estate, 'Resources: 0 added, 0 changed, 1 destroyed', exit 0." \
  "" \
  "So this step builds that estate, the same two instances, and has the" \
  "proxy answer 404 NoSuchKey to every GET of one record - the bulk" \
  "read's, its second look, and the per-key read the run falls back to -" \
  "while the listing goes on naming the key. One store, two answers. The" \
  "run must refuse and say which record, on a plan, on a destroy plan and" \
  "on the destroy itself, and leave the bucket exactly as it found it."
rm -f "$SMOKE_WORK/fail"
PAIR="$SMOKE_WORK/pair"
PAIR_PREFIX="tofu-records/smoke-bulkread-pair/terraform_data/"
MISSED_ADDR='terraform_data.effect["plain"]'
mkdir -p "$PAIR"
cat > "$PAIR/main.tf" <<TFEOF
terraform {
  live {
    estate = "smoke-bulkread-pair"

    record_store "s3" {
      bucket = "$BUCKET"
    }
  }
}

# GitHub issue #1355's estate, as filed.
resource "terraform_data" "effect" {
  for_each = toset(["a.b", "plain"])
  input    = each.key
}
TFEOF
pair() { ( cd "$PAIR" && "$PAIR_BIN" "$@" ); }
# What the bucket holds under the pair's prefix, asked of the emulator
# directly and never through the proxy: "<versions> <delete markers>", and the
# current keys. A destroy that went through writes a delete marker, so an
# unchanged pair of numbers is "nothing was applied" measured in the store.
pair_versions() { awsl s3api list-object-versions --bucket "$BUCKET" --prefix "$PAIR_PREFIX" --query '[length(Versions || `[]`), length(DeleteMarkers || `[]`)]' --output text | tr '\t' ' '; }
pair_keys() { awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix "$PAIR_PREFIX" --query 'Contents[].Key' --output text | tr '\t' '\n'; }
# not_refused answers, in words, why a run is NOT the refusal this step is
# about, and answers nothing when it is. Both arms ask it the same question:
# the ordinary arm fails on any answer, and the BREAK_CROSSCHECK arm requires
# one, so the control is caught by the assertion it is a control for.
not_refused() {
  local rc="$1" out
  out="$(flat <<< "$2")"
  if [ "$rc" = "0" ]; then
    echo "it exited 0"
  elif ! grep -qF "The record store contradicts itself about a record" <<< "$out"; then
    echo "it failed, but not with 'The record store contradicts itself about a record'"
  elif ! grep -qF "$MISSED_ADDR" <<< "$out"; then
    echo "the refusal does not name the address $MISSED_ADDR"
  elif ! grep -qF "$MISSED_KEY" <<< "$out"; then
    echo "the refusal does not name the key $MISSED_KEY"
  elif grep -qE 'Plan: [0-9]+ to add|Destruction complete|Apply complete|Destroy complete' <<< "$out"; then
    echo "it refused and still planned or applied something"
  fi
  return 0
}
# fault_seen requires the proxy to have answered 404 for the record at least
# three times in the run just made (the bulk GET, its second look, the per-key
# read) and to have forwarded the listing, so a refusal can never be credited
# to a fault that was not injected.
fault_seen() {
  local n lists
  n="$(grep -F "$MISSED_KEY" "$SMOKE_WORK/proxy.log" | grep -c ' 404$' || true)"
  lists="$(grep -F 'list-type=2' "$SMOKE_WORK/proxy.log" | grep -c ' 200$' || true)"
  [ "${n:-0}" -ge 3 ] || fail "bulkread" "$1: the proxy answered 404 for the record ${n:-0} times, fewer than the 3 that a bulk GET, its second look and the per-key read make; the fault did not reach every read, so this run measured something else"
  [ "${lists:-0}" -ge 1 ] || fail "bulkread" "$1: the proxy forwarded no listing, so nothing told this run the key exists and there was no contradiction to refuse"
  echo "$1: the record's GET answered 404 $n times; listings forwarded untouched: $lists" | evidence
}

cmd "choudoufu apply -auto-approve   # two instances, through the proxy, nothing failing"
pair init -input=false -no-color >/dev/null 2>&1 || fail "bulkread" "init of the two-instance estate failed"
A_OUT="$(pair apply -auto-approve -input=false -no-color 2>&1)" || fail "bulkread" "the two-instance apply failed: $A_OUT"
grep -q "Resources: 2 added" <<< "$A_OUT" || fail "bulkread" "expected 2 resources: $A_OUT"
MISSED_KEY="$PAIR_PREFIX$(python3 -c 'import base64,sys; print(base64.urlsafe_b64encode(sys.argv[1].encode()).decode().rstrip("="))' "$MISSED_ADDR")"
HELD="$(pair_keys)"
[ "$(grep -c . <<< "$HELD" || true)" = "2" ] || fail "bulkread" "expected 2 record objects under $PAIR_PREFIX, found: $HELD"
grep -qxF "$MISSED_KEY" <<< "$HELD" || fail "bulkread" "$MISSED_ADDR's record key was computed as $MISSED_KEY and the bucket does not hold it, so the fault below would be injected at nothing: $HELD"
BEFORE="$(pair_versions)"
rm -f "$PAIR/.terraform/choudoufu-cache.tfstate"
PC_OUT="$(pair plan -input=false -no-color 2>&1)" || fail "bulkread" "the two-instance control plan failed: $PC_OUT"
grep -q "No changes." <<< "$PC_OUT" || fail "bulkread" "the two-instance control plan was not empty before any fault: $PC_OUT"
echo "two records; the one the proxy will deny: $MISSED_KEY" | evidence
echo "versions and delete markers under the prefix before: $BEFORE" | evidence
echo "$MISSED_KEY -1 404" > "$SMOKE_WORK/fail"

if [ "${BREAK_CROSSCHECK:-0}" = "1" ]; then
  : > "$SMOKE_WORK/proxy.log"
  rm -f "$PAIR/.terraform/choudoufu-cache.tfstate"
  cmd "choudoufu plan   # $MISSED_ADDR's record answers 404, the listing still names it"
  BP_OUT="$(pair plan -input=false -no-color 2>&1)" && BP_RC=0 || BP_RC=$?
  fault_seen "plan"
  BP_WHY="$(not_refused "$BP_RC" "$BP_OUT")"
  [ -n "$BP_WHY" ] || fail "bulkread" "the binary built without the cross-check still refused the plan, so the break did not take and this control proves nothing: $BP_OUT"
  grep -qF "$MISSED_ADDR will be created" <<< "$BP_OUT" \
    || fail "bulkread" "the binary built without the cross-check did not refuse ($BP_WHY) and did not propose creating $MISSED_ADDR either, which is not the defect this control manufactures: $BP_OUT"
  { grep -E 'will be created|^Plan:' <<< "$BP_OUT" || true; } | head -3 | evidence
  : > "$SMOKE_WORK/proxy.log"
  rm -f "$PAIR/.terraform/choudoufu-cache.tfstate"
  cmd "choudoufu apply -destroy -auto-approve   # the same fault"
  BD_OUT="$(pair apply -destroy -auto-approve -input=false -no-color 2>&1)" && BD_RC=0 || BD_RC=$?
  fault_seen "apply -destroy"
  BD_WHY="$(not_refused "$BD_RC" "$BD_OUT")"
  [ -n "$BD_WHY" ] || fail "bulkread" "the binary built without the cross-check still refused the destroy, so the break did not take and this control proves nothing: $BD_OUT"
  [ "$BD_RC" = "0" ] || fail "bulkread" "the broken binary's destroy did not refuse and did not exit 0 either (rc=$BD_RC), which is not #1355's output: $BD_OUT"
  grep -q "Resources: 0 added, 0 changed, 1 destroyed" <<< "$BD_OUT" \
    || fail "bulkread" "the broken binary's destroy exited 0 without reporting '1 destroyed' of two, which is not #1355's output: $BD_OUT"
  LEFT="$(pair_keys)"
  grep -qxF "$MISSED_KEY" <<< "$LEFT" \
    || fail "bulkread" "the broken binary's destroy reported 1 destroyed and $MISSED_ADDR's record is not in the bucket afterwards, so what it missed is not what this control says it missed: $LEFT"
  { grep -E 'Destruction complete|Resources:' <<< "$BD_OUT" || true; } | head -3 | evidence
  echo "exit status of the destroy: $BD_RC; still in the bucket afterwards: $LEFT" | evidence
  proof "caught - without the cross-check the plan proposes CREATING $MISSED_ADDR, whose record is in the bucket, and apply -destroy reports 1 destroyed of two and exits 0 with that record still there. That is GitHub issue #1355's output, manufactured, and the ordinary arm fails on it by name: $BD_WHY."
  exit 0
fi

for MODE in "plan" "plan -destroy" "apply -destroy -auto-approve"; do
  : > "$SMOKE_WORK/proxy.log"
  rm -f "$PAIR/.terraform/choudoufu-cache.tfstate"
  cmd "choudoufu $MODE   # $MISSED_ADDR's record answers 404, the listing still names it"
  # MODE is a command and its flags, so it is meant to split.
  # shellcheck disable=SC2086
  X_OUT="$(pair $MODE -input=false -no-color 2>&1)" && X_RC=0 || X_RC=$?
  fault_seen "$MODE"
  X_WHY="$(not_refused "$X_RC" "$X_OUT")"
  [ -z "$X_WHY" ] || fail "bulkread" "choudoufu $MODE, with a record the listing names and the GET does not find: $X_WHY. On a destroy that is one fewer resource destroyed than the estate holds, under a line reporting success (GitHub issue #1355). $X_OUT"
  { grep -E 'Error:' <<< "$X_OUT" || true; } | head -1 | evidence
done
AFTER="$(pair_versions)"
[ "$AFTER" = "$BEFORE" ] || fail "bulkread" "three refused runs changed the bucket: versions and delete markers under $PAIR_PREFIX were '$BEFORE' and are '$AFTER'"
STILL="$(pair_keys)"
[ "$STILL" = "$HELD" ] || fail "bulkread" "three refused runs changed which records the bucket holds: before '$HELD', after '$STILL'"
echo "versions and delete markers under the prefix after: $AFTER" | evidence
proof "refused three times over, naming $MISSED_ADDR and its key each time, and the bucket holds what it held. A record the store lists and will not hand over is not an absent one."

step "6. teardown"
rm -f "$SMOKE_WORK/fail"
cmd "choudoufu apply -destroy -auto-approve   # the two-instance estate, with the fault lifted"
PD_OUT="$(pair apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "bulkread" "the two-instance teardown failed: $PD_OUT"
destroyed_exactly bulkread 2 "$PD_OUT"
cmd "choudoufu apply -destroy -auto-approve"
D_OUT="$(run apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "bulkread" "teardown failed: $D_OUT"
destroyed_exactly bulkread "$N" "$D_OUT"
proof "gone, both estates, each by its full count."

echo "  What you watched: $N records read through a proxy, one GET failed once"
echo "  and then always, and no plan at any point proposing to create a"
echo "  resource whose record was merely unreadable; then a second estate of"
echo "  two, one record listed and answered 404, and every run refusing over"
echo "  it instead of planning a destroy one short."
