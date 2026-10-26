# two-applies-disjoint-records
# CLAIM 2 (aws) - Nothing is held. ~3 min.
#
# This proof: Two applies changing different resources of one estate both
# land, and each writes only the record it changed (live/CONCURRENCY.md,
# "Two runs change different resources"; GitHub issues #1938 and #2255).
#
# A draft until its first green run on both arms (drafts/README.md). The
# same-record half of the record-store claim is two-writers-one-record; this
# is the disjoint half, unit-tested until now by
# internal/live/projection/writeback_changedonly_1938_test.go and
# writeinstance_disjoint_1944_test.go.

SMOKE_WORK="$SMOKE_WORKROOT/disjoint"
ESTATE="smoke-disjoint"
BUCKET="smoke-disjoint-records"
RECORD_PATH="tofu-records/$ESTATE/terraform_data/"
mkdir -p "$SMOKE_WORK/a" "$SMOKE_WORK/b" "$SMOKE_WORK/pa" "$SMOKE_WORK/pb"; export SMOKE_WORK

# record_key <address>: the record's object key, as projection.RecordKey
# builds it (unpadded URL-safe base64 of the address, short enough here to
# need no chunking).
record_key() {
  python3 -c 'import base64, sys; print(sys.argv[1] + base64.urlsafe_b64encode(sys.argv[2].encode()).decode().rstrip("="))' "$RECORD_PATH" "$1"
}
LEFT_KEY="$(record_key terraform_data.left)"
RIGHT_KEY="$(record_key terraform_data.right)"

# write_config <dir> <left input> <right input>. Checkouts a and b are one
# estate: same estate name, same bucket, both resources declared in each.
write_config() {
  cat > "$1/main.tf" <<TFEOF
terraform {
  live {
    estate = "$ESTATE"

    record_store "s3" {
      bucket = "$BUCKET"
    }

    retry {
      max_attempts = 1
    }
  }
}

resource "terraform_data" "left" {
  input = "$2"
}

resource "terraform_data" "right" {
  input = "$3"
}
TFEOF
}

step "the claim"
explain \
  "Two applies of one estate, started together, each change a different" \
  "resource. Nothing is locked, and nothing needs to be: each apply writes" \
  "back only the record of the resource it changed, under If-Match, so" \
  "the two never touch the same object and both land. Before #1938 every" \
  "apply rewrote every record, and two such applies collided on a record" \
  "neither had changed."

# The binary under test. BREAK=1 swaps in one that writes back every record,
# changed or not, which is the write-back #1938 removed.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a write-back of every record must be caught"
  explain \
    "The break is in the binary: the record store's check that skips a" \
    "record this run would write back unchanged is turned off. Built with" \
    "go build -overlay, so the source tree is never touched; it needs this" \
    "checkout and Go."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "disjoint" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "disjoint" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/live/projection/record.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/record.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
old = "\tif base, ok := s.baselineAt(key, expectedVersion); ok {\n"
assert src.count(old) == 1, "the break patch no longer matches RecordStore.mergeEnvelope's #1938 check"
open(sys.argv[2], "w").write(src.replace(old, "\tif base, ok := s.baselineAt(key, expectedVersion); ok && false { // BREAK: write back every record\n"))
PYEOF
  [ -s "$SMOKE_WORK/break/record.go" ] || fail "disjoint" "the break patch did not apply to $SRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/record.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # every record written back"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "disjoint" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi
record_version() { # <key>: the S3 version the bucket holds for it now
  awsl s3api head-object --bucket "$BUCKET" --key "$1" --query VersionId --output text
}
record_holds() { # <key> <value>: whether the record holds that input
  awsl s3api get-object --bucket "$BUCKET" --key "$1" "$SMOKE_WORK/record.json" >/dev/null || return 1
  grep -q "$2" "$SMOKE_WORK/record.json"
}

step "1. one estate, two records, two checkouts of it"
stack_up
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "disjoint" "could not create the bucket"
awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled >/dev/null
awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null
write_config "$SMOKE_WORK/a" seed-left seed-right
write_config "$SMOKE_WORK/b" seed-left seed-right
logged two-applies-disjoint-records-a-init "disjoint" "init failed in a" -- in_dir "$SMOKE_WORK/a" "$RUN_BIN" init -input=false -no-color
logged two-applies-disjoint-records-b-init "disjoint" "init failed in b" -- in_dir "$SMOKE_WORK/b" "$RUN_BIN" init -input=false -no-color
cmd "choudoufu apply -auto-approve   # in checkout a"
OUT="$(cd "$SMOKE_WORK/a" && "$RUN_BIN" apply -auto-approve -input=false -no-color 2>&1)" || fail "disjoint" "the seeding apply failed: $OUT"
record_holds "$LEFT_KEY" seed-left || fail "disjoint" "the left record ($LEFT_KEY) does not hold seed-left after the seeding apply"
record_holds "$RIGHT_KEY" seed-right || fail "disjoint" "the right record ($RIGHT_KEY) does not hold seed-right after the seeding apply"
LEFT_V0="$(record_version "$LEFT_KEY")"; RIGHT_V0="$(record_version "$RIGHT_KEY")"
# Checkout b plans from the bucket, not from a's cache.
rm -f "$SMOKE_WORK/a/.terraform/choudoufu-cache.tfstate" "$SMOKE_WORK/b/.terraform/choudoufu-cache.tfstate"
echo "left $LEFT_KEY at $LEFT_V0, right $RIGHT_KEY at $RIGHT_V0" | evidence
proof "two record-backed resources, one record each, both in the bucket."

step "2. one proxy per checkout, so every write is attributed"
explain \
  "Each checkout talks to the bucket through its own proxy, and each" \
  "proxy logs every request with the conditional-write header a PUT" \
  "carried. Each proxy also holds the first record write it sees until" \
  "both checkouts have sent one: a record is written only after its" \
  "apply step, so two held writes are two applies that both planned" \
  "against the seeded versions before either wrote."
python3 "$SMOKE_DIR/s3proxy.py" "$FLOCI_PORT" "$SMOKE_WORK/pa" 2>"$SMOKE_WORKROOT/logs/disjoint-proxy-a.err" &
PROXY_A=$!
python3 "$SMOKE_DIR/s3proxy.py" "$FLOCI_PORT" "$SMOKE_WORK/pb" 2>"$SMOKE_WORKROOT/logs/disjoint-proxy-b.err" &
PROXY_B=$!
trap 'kill $PROXY_A $PROXY_B 2>/dev/null || true; cleanup' EXIT
for _ in $(seq 1 50); do [ -s "$SMOKE_WORK/pa/proxy.port" ] && [ -s "$SMOKE_WORK/pb/proxy.port" ] && break; sleep 0.1; done
{ [ -s "$SMOKE_WORK/pa/proxy.port" ] && [ -s "$SMOKE_WORK/pb/proxy.port" ]; } || fail "disjoint" "a proxy never started"
URL_A="http://localhost:$(cat "$SMOKE_WORK/pa/proxy.port")"
URL_B="http://localhost:$(cat "$SMOKE_WORK/pb/proxy.port")"
for p in pa pb; do
  rm -f "$SMOKE_WORK/$p/held" "$SMOKE_WORK/$p/release"
  : > "$SMOKE_WORK/$p/proxy.log"
  echo "$RECORD_PATH" > "$SMOKE_WORK/$p/hold"
done
proof "every record-store request from here on goes through a proxy that names its checkout."

step "3. the race - a changes left, b changes right, at the same moment"
write_config "$SMOKE_WORK/a" from-a-left seed-right
write_config "$SMOKE_WORK/b" seed-left from-b-right
cmd "(apply in a &) ; (apply in b &)"
# `&& rc=0 || rc=$?`, never `; echo $?`: smoke.sh runs under set -e
# (two-writers-one-record, #1590/#1646).
( cd "$SMOKE_WORK/a" && { AWS_ENDPOINT_URL="$URL_A" "$RUN_BIN" apply -auto-approve -input=false -no-color > "$SMOKE_WORK/a.out" 2>&1 && rc=0 || rc=$?; echo "$rc" > "$SMOKE_WORK/a.rc"; } ) &
PID_A=$!
( cd "$SMOKE_WORK/b" && { AWS_ENDPOINT_URL="$URL_B" "$RUN_BIN" apply -auto-approve -input=false -no-color > "$SMOKE_WORK/b.out" 2>&1 && rc=0 || rc=$?; echo "$rc" > "$SMOKE_WORK/b.rc"; } ) &
PID_B=$!
held() { if [ -f "$SMOKE_WORK/$1/held" ]; then grep -c '' "$SMOKE_WORK/$1/held"; else echo 0; fi; }
for _ in $(seq 1 1200); do
  [ "$(held pa)" -ge 1 ] && [ "$(held pb)" -ge 1 ] && break
  sleep 0.05
done
if ! { [ "$(held pa)" -ge 1 ] && [ "$(held pb)" -ge 1 ]; }; then
  kill "$PID_A" "$PID_B" 2>/dev/null || true
  fail "disjoint" "both applies did not reach a record write within 60s (a held $(held pa), b held $(held pb)), so there was no race to judge: a: $(tail -3 "$SMOKE_WORK/a.out" 2>/dev/null) b: $(tail -3 "$SMOKE_WORK/b.out" 2>/dev/null)"
fi
# Release every write, in arrival order, on both proxies.
for p in pa pb; do seq 1 20 | tr '\n' ' ' > "$SMOKE_WORK/$p/release"; done
for _ in $(seq 1 1200); do
  [ -s "$SMOKE_WORK/a.rc" ] && [ -s "$SMOKE_WORK/b.rc" ] && break
  sleep 0.05
done
if ! { [ -s "$SMOKE_WORK/a.rc" ] && [ -s "$SMOKE_WORK/b.rc" ]; }; then
  kill "$PID_A" "$PID_B" 2>/dev/null || true
  fail "disjoint" "an apply was still running 60s after the writes were released"
fi
wait "$PID_A" "$PID_B" 2>/dev/null || true
for p in pa pb; do rm -f "$SMOKE_WORK/$p/hold"; done
A_RC="$(cat "$SMOKE_WORK/a.rc")"; B_RC="$(cat "$SMOKE_WORK/b.rc")"

# puts <proxy dir> <key>: the PUT lines a proxy logged for one record.
puts() { grep -E "^PUT [^ ]*$2 " "$SMOKE_WORK/$1/proxy.log" || true; }
A_LEFT="$(puts pa "$LEFT_KEY")"; A_RIGHT="$(puts pa "$RIGHT_KEY")"
B_LEFT="$(puts pb "$LEFT_KEY")"; B_RIGHT="$(puts pb "$RIGHT_KEY")"
echo "--- checkout a's record writes ---" | evidence
grep -E "^PUT " "$SMOKE_WORK/pa/proxy.log" | evidence
echo "--- checkout b's record writes ---" | evidence
grep -E "^PUT " "$SMOKE_WORK/pb/proxy.log" | evidence

if [ "${BREAK:-0}" = "1" ]; then
  if [ -n "$A_RIGHT" ] || [ -n "$B_LEFT" ]; then
    [ -n "$A_RIGHT" ] && echo "checkout a rewrote the right record it never changed: $A_RIGHT" | evidence
    [ -n "$B_LEFT" ] && echo "checkout b rewrote the left record it never changed: $B_LEFT" | evidence
    proof "caught - with every record written back, an apply rewrote $( [ -n "$A_RIGHT" ] && echo "terraform_data.right ($RIGHT_KEY)" || echo "terraform_data.left ($LEFT_KEY)" ), a record it did not change. That is the write that made two disjoint applies collide."
    exit 0
  fi
  fail "disjoint" "the binary built to write back every record wrote back only the changed ones, so the break did not take and this control proves nothing"
fi

[ "$A_RC" = "0" ] || fail "disjoint" "apply a (changing left) failed: $(cat "$SMOKE_WORK/a.out")"
[ "$B_RC" = "0" ] || fail "disjoint" "apply b (changing right) failed: $(cat "$SMOKE_WORK/b.out")"
proof "both applies exited 0."

step "4. both changes landed, read from the bucket"
cmd "aws s3api get-object / head-object   # both records"
record_holds "$LEFT_KEY" from-a-left || fail "disjoint" "the left record does not hold a's change (from-a-left): $(cat "$SMOKE_WORK/record.json" 2>/dev/null)"
record_holds "$RIGHT_KEY" from-b-right || fail "disjoint" "the right record does not hold b's change (from-b-right): $(cat "$SMOKE_WORK/record.json" 2>/dev/null)"
LEFT_V1="$(record_version "$LEFT_KEY")"; RIGHT_V1="$(record_version "$RIGHT_KEY")"
[ "$LEFT_V1" != "$LEFT_V0" ] || fail "disjoint" "the left record is still at version $LEFT_V0"
[ "$RIGHT_V1" != "$RIGHT_V0" ] || fail "disjoint" "the right record is still at version $RIGHT_V0"
echo "left: from-a-left, version $LEFT_V0 -> $LEFT_V1; right: from-b-right, version $RIGHT_V0 -> $RIGHT_V1" | evidence
proof "each record holds its writer's change and moved to a new version."

step "5. each apply wrote only its own record, conditionally"
explain \
  "Read off the wire: a's proxy saw a conditional PUT for left and none" \
  "for right, b's the reverse. A write for the other's record is the" \
  "pre-#1938 write-back, and the collision it causes."
[ -n "$A_LEFT" ] || fail "disjoint" "checkout a sent no PUT for the left record it changed"
[ -n "$B_RIGHT" ] || fail "disjoint" "checkout b sent no PUT for the right record it changed"
[ -z "$A_RIGHT" ] || fail "disjoint" "checkout a rewrote the right record, which it did not change: $A_RIGHT"
[ -z "$B_LEFT" ] || fail "disjoint" "checkout b rewrote the left record, which it did not change: $B_LEFT"
if grep -qv " if-match: " <<< "$A_LEFT"; then fail "disjoint" "a write of the left record carried no If-Match: $A_LEFT"; fi
if grep -qv " if-match: " <<< "$B_RIGHT"; then fail "disjoint" "a write of the right record carried no If-Match: $B_RIGHT"; fi
proof "a wrote left under If-Match and nothing for right; b wrote right under If-Match and nothing for left."

step "6. teardown"
cmd "choudoufu apply -destroy -auto-approve"
write_config "$SMOKE_WORK/a" from-a-left from-b-right
D_OUT="$(cd "$SMOKE_WORK/a" && "$RUN_BIN" apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "disjoint" "teardown failed: $D_OUT"
destroyed_exactly disjoint 2 "$D_OUT"
proof "gone."

echo "  What you watched: two applies of one estate, each changing a different"
echo "  resource at the same moment, both landed; each wrote back only the"
echo "  record it changed, under If-Match, and never the other's."
