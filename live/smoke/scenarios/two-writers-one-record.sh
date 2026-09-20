# two-writers-one-record
# CLAIM 32 - Two writers, one record: the loser is named, nothing is clobbered, and nothing is held. ~3 min.

SMOKE_WORK="$SMOKE_WORKROOT/writerace"
BUCKET="smoke-writerace-records"
RECORD_PATH="tofu-records/smoke-writerace/terraform_data/"
mkdir -p "$SMOKE_WORK/a" "$SMOKE_WORK/b"; export SMOKE_WORK

# write_config <dir> <input>. Writers A and B are two checkouts of one estate:
# same estate name, same bucket, same resource address, so they contend for
# one record object.
write_config() {
  cat > "$1/main.tf" <<TFEOF
terraform {
  live {
    estate = "smoke-writerace"

    record_store "s3" {
      bucket = "$BUCKET"
    }

    retry {
      max_attempts = 1
    }
  }
}

resource "terraform_data" "effect" {
  input = "$2"
}
TFEOF
}

step "the claim"
explain \
  "Nothing here takes a lock. Two applies that touch the same record" \
  "both go ahead, and the record store settles it: every write is one" \
  "conditional PUT carrying the version it read (If-Match), so exactly" \
  "one of two racing writers lands and the other is told, by name, which" \
  "version it expected and which it found. Claim 2 measures this at the" \
  "platform's API; this is the same thesis at the store the design now" \
  "leans on entirely. A conditional write is not a lock: it holds nothing" \
  "across operations, so there is nothing a crash could leave stuck."

# The binary under test. BREAK=1 swaps in one whose record writes carry no
# precondition.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - an unconditional write must be caught as a clobber"
  explain \
    "The corruption is in the binary: the S3 store's PUT drops its" \
    "If-Match. Built with go build -overlay, so the source tree is never" \
    "touched; it needs this checkout and Go."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "writerace" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "writerace" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/live/staterecord/s3.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/s3.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
old = "\t\tinput.IfMatch = aws.String(expectedVersion)\n"
assert src.count(old) == 1, "the break patch no longer matches S3Store.PutIfVersion"
open(sys.argv[2], "w").write(src.replace(old, "\t\t_ = expectedVersion // BREAK: an update with no precondition\n"))
PYEOF
  [ -s "$SMOKE_WORK/break/s3.go" ] || fail "writerace" "the break patch did not apply to $SRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/s3.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # PutObject without If-Match"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "writerace" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }
# lockish_keys lists every key in the bucket whose name looks like a lock.
# It replaced a grep for "Acquiring state lock" in the writers' output, which
# could not fire: internal/command/clistate/state.go prints that line only
# after a lock has been outstanding for 400ms, so a lock taken and released
# quickly - which is every lock a passing run of this scenario would take -
# leaves nothing in the output to find (#1379). The bucket is where a lock
# this store took would have to live, and an object is there or it is not.
lockish_keys() {
  awsl s3api list-objects-v2 --bucket "$BUCKET" --query 'Contents[].Key' --output text | tr '\t' '\n' | grep -iE 'lock|\.tflock' || true
}
record_input() { # the input value the record in the bucket holds right now
  local key
  key="$(awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix "$RECORD_PATH" --query 'Contents[0].Key' --output text)"
  awsl s3api get-object --bucket "$BUCKET" --key "$key" "$SMOKE_WORK/record.json" >/dev/null
  python3 - "$SMOKE_WORK/record.json" <<'PYEOF'
import json, re, sys
raw = open(sys.argv[1]).read()
found = sorted(set(re.findall(r"from-[ab]-r[0-9]+|seed", raw)))
print(",".join(found) if found else "?")
PYEOF
}

step "1. one estate, one record, two checkouts of it"
stack_up
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "writerace" "could not create the bucket"
awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled >/dev/null
awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >/dev/null
awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null
write_config "$SMOKE_WORK/a" seed
write_config "$SMOKE_WORK/b" seed
( cd "$SMOKE_WORK/a" && "$RUN_BIN" init -input=false -no-color >/dev/null 2>&1 ) || fail "writerace" "init failed in a"
( cd "$SMOKE_WORK/b" && "$RUN_BIN" init -input=false -no-color >/dev/null 2>&1 ) || fail "writerace" "init failed in b"
cmd "choudoufu apply -auto-approve   # in checkout a"
OUT="$(cd "$SMOKE_WORK/a" && "$RUN_BIN" apply -auto-approve -input=false -no-color 2>&1)" || fail "writerace" "the seeding apply failed: $OUT"
[ "$(record_input)" = "seed" ] || fail "writerace" "the seeded record does not hold the seed value: $(record_input)"
proof "one record-backed resource, its record in the bucket holding \"seed\"."

step "2. the proxy that makes the race a race"
explain \
  "Two applies started together usually do not overlap: one finishes its" \
  "write before the other has read, and that is a sequence, not a race." \
  "The proxy holds each writer's PUT to the record until BOTH have" \
  "arrived, so both were planned against the same version, and then lets" \
  "them through one at a time in a chosen order."
python3 "$SMOKE_DIR/s3proxy.py" "$FLOCI_PORT" "$SMOKE_WORK" 2>"$SMOKE_WORKROOT/logs/writerace-proxy.err" &
PROXY_PID=$!
trap 'kill $PROXY_PID 2>/dev/null || true; cleanup' EXIT
for _ in $(seq 1 50); do [ -s "$SMOKE_WORK/proxy.port" ] && break; sleep 0.1; done
[ -s "$SMOKE_WORK/proxy.port" ] || fail "writerace" "the proxy never started"
PROXY_URL="http://localhost:$(cat "$SMOKE_WORK/proxy.port")"
proof "every record-store request from here on goes through it."

held_count() { if [ -f "$SMOKE_WORK/held" ]; then wc -l < "$SMOKE_WORK/held" | tr -d ' '; else echo 0; fi; }

# race <round> <release-order>: both writers apply at once through the proxy.
# Sets A_RC, B_RC, A_OUT, B_OUT, and FIRST (the marker of the PUT released first).
race() {
  local round="$1" order="$2"
  write_config "$SMOKE_WORK/a" "from-a-r$round"
  write_config "$SMOKE_WORK/b" "from-b-r$round"
  rm -f "$SMOKE_WORK/held" "$SMOKE_WORK/release" "$SMOKE_WORK/a.rc" "$SMOKE_WORK/b.rc" "$SMOKE_WORK/a/.terraform/choudoufu-cache.tfstate" "$SMOKE_WORK/b/.terraform/choudoufu-cache.tfstate"
  echo "from-a-r$round from-b-r$round" > "$SMOKE_WORK/markers"
  echo "$RECORD_PATH" > "$SMOKE_WORK/hold"
  # `&& rc=0 || rc=$?`, not `; echo $?`: smoke.sh runs under set -e, and the
  # losing writer exits non-zero, which would end its subshell before the
  # exit code was written - and the loser's is the one this claim is about.
  ( cd "$SMOKE_WORK/a" && { AWS_ENDPOINT_URL="$PROXY_URL" "$RUN_BIN" apply -auto-approve -input=false -no-color > "$SMOKE_WORK/a.out" 2>&1 && rc=0 || rc=$?; echo "$rc" > "$SMOKE_WORK/a.rc"; } ) &
  local pa=$!
  ( cd "$SMOKE_WORK/b" && { AWS_ENDPOINT_URL="$PROXY_URL" "$RUN_BIN" apply -auto-approve -input=false -no-color > "$SMOKE_WORK/b.out" 2>&1 && rc=0 || rc=$?; echo "$rc" > "$SMOKE_WORK/b.rc"; } ) &
  local pb=$!
  local i
  for i in $(seq 1 600); do
    [ "$(held_count)" = "2" ] && break
    sleep 0.05
  done
  [ "$(held_count)" = "2" ] \
    || { kill $pa $pb 2>/dev/null; fail "writerace" "[round $round] only $(held_count) writer(s) reached the record write within 30s, so there was no race to judge: $(cat "$SMOKE_WORK/a.out" "$SMOKE_WORK/b.out" 2>/dev/null | tail -5)"; }
  # Release in arrival order, or reversed.
  if [ "$order" = "reversed" ]; then echo "2 1" > "$SMOKE_WORK/release"; FIRST="$(sed -n 2p "$SMOKE_WORK/held" | cut -d' ' -f2)"
  else echo "1 2" > "$SMOKE_WORK/release"; FIRST="$(sed -n 1p "$SMOKE_WORK/held" | cut -d' ' -f2)"; fi
  # Bounded, never a bare `wait`: a writer that is still waiting on the proxy
  # would hang the scenario, and a hang is worse than a failure. It happened
  # while this scenario was being written - a proxy that numbered arrivals
  # across rounds left round two's writers waiting for a turn forever.
  for i in $(seq 1 1200); do
    [ -s "$SMOKE_WORK/a.rc" ] && [ -s "$SMOKE_WORK/b.rc" ] && break
    sleep 0.05
  done
  if ! { [ -s "$SMOKE_WORK/a.rc" ] && [ -s "$SMOKE_WORK/b.rc" ]; }; then
    kill $pa $pb 2>/dev/null || true
    pkill -f "$RUN_BIN apply" 2>/dev/null || true
    fail "writerace" "[round $round, $order] a writer was still running 60s after both PUTs were released (held: $(tr '\n' ';' < "$SMOKE_WORK/held"), release: $(cat "$SMOKE_WORK/release"))"
  fi
  wait $pa $pb 2>/dev/null || true
  rm -f "$SMOKE_WORK/hold"
  [ -s "$SMOKE_WORK/a.rc" ] && [ -s "$SMOKE_WORK/b.rc" ] || fail "writerace" "[round $round] a writer finished without recording its exit code, so the round cannot be judged"
  A_RC="$(cat "$SMOKE_WORK/a.rc")"; B_RC="$(cat "$SMOKE_WORK/b.rc")"
  A_OUT="$(cat "$SMOKE_WORK/a.out")"; B_OUT="$(cat "$SMOKE_WORK/b.out")"
}

ROUNDS=6
step "3. the race, $ROUNDS times, at both orderings"
explain \
  "Each round both checkouts change the same resource to a different" \
  "value and apply at once. Odd rounds release the PUTs in the order" \
  "they arrived, even rounds in reverse, so the writer that loses is not" \
  "always the one that was slower to get there."
CLOBBERS=0; NAMED=0
for round in $(seq 1 $ROUNDS); do
  order="arrival"; [ $((round % 2)) = 0 ] && order="reversed"
  race "$round" "$order"
  HOLDS="$(record_input)"
  ROUND_LOCKS="$(lockish_keys)"
  [ -z "$ROUND_LOCKS" ] || fail "writerace" "[round $round] a lock-shaped object is in the bucket: $ROUND_LOCKS"
  if [ "$A_RC" = "0" ] && [ "$B_RC" = "0" ]; then
    CLOBBERS=$((CLOBBERS+1))
    if [ "${BREAK:-0}" = "1" ]; then
      echo "round $round ($order): both applies reported success; the record holds $HOLDS" | evidence
      proof "caught - with the precondition gone both writers \"won\". One envelope silently replaced the other and neither run said a word. That is the clobber."
      exit 0
    fi
    fail "writerace" "[round $round, $order] BOTH applies succeeded against one record version: last write wins, silently. a: $(tail -2 <<< "$A_OUT") b: $(tail -2 <<< "$B_OUT")"
  fi
  [ "$A_RC" != "0" ] && [ "$B_RC" != "0" ] && fail "writerace" "[round $round, $order] both applies failed; exactly one should land. a: $A_OUT b: $B_OUT"
  if [ "$A_RC" = "0" ]; then WINNER="from-a-r$round"; LOSER_OUT="$B_OUT"; LOSER=b; else WINNER="from-b-r$round"; LOSER_OUT="$A_OUT"; LOSER=a; fi
  [ "$WINNER" = "$FIRST" ] || fail "writerace" "[round $round, $order] the PUT released first carried $FIRST but the apply that succeeded wrote $WINNER"
  [ "$HOLDS" = "$WINNER" ] || fail "writerace" "[round $round, $order] the winner wrote $WINNER but the record holds $HOLDS"
  L_FLAT="$(flat <<< "$LOSER_OUT")"
  grep -q "Record store write conflict" <<< "$L_FLAT" || fail "writerace" "[round $round, $order] the loser failed without naming a record store write conflict: $LOSER_OUT"
  # Named means BOTH versions, read out of the message and compared: the one
  # this run planned against and the one it found. A conflict that named one,
  # or the same value twice, would tell the operator nothing.
  VERSIONS="$(python3 - "$L_FLAT" <<'PYEOF'
import re, sys
m = re.search(r'expected version (\S+) and the store now holds version (\S+?)\.? ', sys.argv[1] + " ")
print("%s %s" % (m.group(1), m.group(2)) if m else "")
PYEOF
)"
  EXPECTED="${VERSIONS%% *}"; FOUND="${VERSIONS##* }"
  [ -n "$VERSIONS" ] && [ -n "$EXPECTED" ] && [ -n "$FOUND" ] && [ "$EXPECTED" != "$FOUND" ] \
    || fail "writerace" "[round $round, $order] the conflict does not name two different versions (expected '$EXPECTED', found '$FOUND'): $LOSER_OUT"
  grep -q "Nothing was overwritten" <<< "$L_FLAT" || fail "writerace" "[round $round, $order] the conflict does not say nothing was overwritten: $LOSER_OUT"
  NAMED=$((NAMED+1))
  echo "round $round ($order): $WINNER landed; writer $LOSER was refused, expected $EXPECTED, found $FOUND" | evidence
done
[ "${BREAK:-0}" = "1" ] && fail "writerace" "the binary built with no write precondition never clobbered in $ROUNDS rounds, so the break did not take and this control proves nothing"
proof "$ROUNDS races, $NAMED named conflicts, $CLOBBERS clobbers, and no lock-shaped object in the bucket after any round. The record always holds the value of the PUT that was judged first."

step "4. the loser's recovery is an ordinary re-plan"
explain \
  "No unlock, no repair verb. The writer that lost plans again, sees" \
  "the record as the winner left it, and applies over it."
cmd "choudoufu plan && choudoufu apply -auto-approve   # in the losing checkout"
R_PLAN="$(cd "$SMOKE_WORK/$LOSER" && "$RUN_BIN" plan -input=false -no-color 2>&1)" || fail "writerace" "the loser's re-plan failed: $R_PLAN"
grep -E '^Plan:' <<< "$R_PLAN" | head -1 | evidence
grep -q "1 to change" <<< "$R_PLAN" || fail "writerace" "the loser's re-plan did not propose its own change over the winner's record: $R_PLAN"
R_APPLY="$(cd "$SMOKE_WORK/$LOSER" && "$RUN_BIN" apply -auto-approve -input=false -no-color 2>&1)" || fail "writerace" "the loser's re-apply failed: $R_APPLY"
[ "$(record_input)" = "from-$LOSER-r$ROUNDS" ] || fail "writerace" "after the loser's re-apply the record holds $(record_input), want from-$LOSER-r$ROUNDS"
proof "converged on the second try, the way any plan made against a changed world does."

step "5. a writer killed mid-write strands nothing"
explain \
  "Writer a is killed with SIGKILL while its PUT is in the proxy's hands," \
  "and the PUT is then thrown away: the worst moment a crash can pick." \
  "With a lock this is where force-unlock comes out. Here writer b simply" \
  "applies."
write_config "$SMOKE_WORK/a" "from-a-r99"
write_config "$SMOKE_WORK/b" "from-b-r99"
rm -f "$SMOKE_WORK/held" "$SMOKE_WORK/release"
echo "from-a-r99 from-b-r99" > "$SMOKE_WORK/markers"
echo "$RECORD_PATH" > "$SMOKE_WORK/hold"
# Emptied here so every proxy line read below belongs to this step.
: > "$SMOKE_WORK/proxy.log"
PRE_KILL="from-$LOSER-r$ROUNDS"
( cd "$SMOKE_WORK/a" && AWS_ENDPOINT_URL="$PROXY_URL" exec "$RUN_BIN" apply -auto-approve -input=false -no-color > "$SMOKE_WORK/a.out" 2>&1 ) &
KILL_PID=$!
for _ in $(seq 1 600); do [ -s "$SMOKE_WORK/held" ] && break; sleep 0.05; done
[ -s "$SMOKE_WORK/held" ] || fail "writerace" "writer a never reached its record write, so it was not killed mid-write"
cmd "kill -9 <writer a>   # its PUT is held, unanswered"
kill -9 $KILL_PID 2>/dev/null; wait $KILL_PID 2>/dev/null || true
echo drop > "$SMOKE_WORK/release"; sleep 0.3
rm -f "$SMOKE_WORK/hold" "$SMOKE_WORK/release"
BEFORE_B="$(record_input)"
# The two assertions this step used to print and not make (#1379). BEFORE_B
# was captured, echoed and never compared, so the sentence below held whether
# the killed writer's PUT had landed or not: with the proxy forwarding the
# held PUT instead of dropping it, this step read "record before writer b:
# from-a-r99" and the scenario still printed PASS.
[ "$BEFORE_B" = "$PRE_KILL" ] \
  || fail "writerace" "the killed writer's change LANDED: the record holds $BEFORE_B, and step 4 left $PRE_KILL. A PUT that was never answered must not be in the store."
DROPPED="$(grep -c "^PUT /${RECORD_PATH}.* dropped$" "$SMOKE_WORK/proxy.log" || true)"
FORWARDED="$(grep -E "^PUT /${RECORD_PATH}" "$SMOKE_WORK/proxy.log" | grep -vc 'dropped$' || true)"
[ "$DROPPED" -ge 1 ] \
  || fail "writerace" "the proxy's log records no dropped PUT to the record, so writer a was not killed with its write in the proxy's hands and nothing here was measured: $(cat "$SMOKE_WORK/proxy.log")"
[ "$FORWARDED" = "0" ] \
  || fail "writerace" "$FORWARDED PUT(s) to the record were forwarded after writer a was killed; its write was not thrown away, so this step is not measuring a crash mid-write: $(grep -E "^PUT /${RECORD_PATH}" "$SMOKE_WORK/proxy.log")"
LOCKISH="$(lockish_keys)"
[ -z "$LOCKISH" ] || fail "writerace" "the killed writer left something lock-shaped in the bucket: $LOCKISH"
cmd "choudoufu apply -auto-approve   # writer b, straight after, no unlock of any kind"
K_OUT="$(cd "$SMOKE_WORK/b" && "$RUN_BIN" apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "writerace" "writer b could not apply after writer a was killed mid-write: $K_OUT"
[ "$(record_input)" = "from-b-r99" ] || fail "writerace" "after writer b's apply the record holds $(record_input), want from-b-r99"
echo "the killed writer's PUT: $DROPPED dropped, $FORWARDED forwarded" | evidence
echo "record before writer b: $BEFORE_B, which is what step 4 left; after: $(record_input); lock-shaped keys in the bucket: none" | evidence
proof "the killed writer's change never landed - the record still held $PRE_KILL, and the proxy's log says its PUT was thrown away rather than forwarded - and nothing of it remained to clear. A conditional write keeps nothing between operations, so there is nothing for a crash to leave held."

step "6. teardown"
cmd "choudoufu apply -destroy -auto-approve"
D_OUT="$(cd "$SMOKE_WORK/b" && "$RUN_BIN" apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "writerace" "teardown failed: $D_OUT"
destroyed_exactly writerace 1 "$D_OUT"
proof "gone."

echo "  What you watched: two writers raced for one record $ROUNDS times and"
echo "  exactly one landed every time, the other told which version it"
echo "  expected and which it found; the loser re-planned and converged; and"
echo "  a writer killed at the worst moment left nothing to unlock."
