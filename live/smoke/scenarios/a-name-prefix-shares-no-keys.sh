# a-name-prefix-shares-no-keys
# CLAIM 28 - Two estates whose names prefix one another share a bucket and none of each other's keys. ~1 min.

SMOKE_WORK="$SMOKE_WORKROOT/nameprefix"
BUCKET="smoke-shared-records"
mkdir -p "$SMOKE_WORK/prod" "$SMOKE_WORK/prod-eu"; export SMOKE_WORK
for estate in prod prod-eu; do
  cat > "$SMOKE_WORK/$estate/main.tf" <<TFEOF
terraform {
  live {
    estate = "smoke-$estate"

    record_store "s3" {
      bucket = "$BUCKET"
    }
  }
}

resource "terraform_data" "effect" {
  input = "$estate"
}
TFEOF
done

# wire_log runs one choudoufu command in an estate's directory with the
# SDK's request log captured to $3, whatever SMOKE_INSTRUMENT says: the
# requests ARE this claim's evidence, so they are not optional here.
wire_log() {
  local dir="$1" bin="$2" log="$3"; shift 3
  ( cd "$dir" && TF_LOG=debug TF_LOG_PATH="$log" "$bin" "$@" )
}

step "the claim"
explain \
  "The bucket backend puts every estate in one bucket, one key prefix" \
  "each. S3's LIST matches a plain string prefix, not a path: ask for" \
  "tofu-records/smoke-prod and you are also asking for" \
  "tofu-records/smoke-prod-eu. Estate names prefix one another all the" \
  "time (prod, prod-eu), so an estate prefix with no trailing delimiter" \
  "hands one estate its neighbour's keys - and then its neighbour's" \
  "records, secrets included, because a run bulk-reads what it lists." \
  "An IAM tag condition cannot stop the listing: a LIST touches no" \
  "object, so s3:prefix is the only condition key there is. The" \
  "delimiter is the whole defence, and this claim reads it off the wire."

step "1. two estates, one bucket"
stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "nameprefix" "could not create the shared bucket"
# A bucket the bucket contract accepts (claim 29). This scenario was written
# before that contract existed and made a bare bucket, which an estate's first
# contact has refused ever since; nothing re-ran it and it stayed red from
# #1339 until this was added. The three settings have nothing to do with what
# is measured here, which is why they are set once and never mentioned again.
awsl s3api put-bucket-versioning --bucket "$BUCKET" --versioning-configuration Status=Enabled >/dev/null \
  || fail "nameprefix" "could not enable versioning on the shared bucket"
awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration '{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}' >/dev/null \
  || fail "nameprefix" "could not set the lifecycle rule on the shared bucket"
awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true >/dev/null \
  || fail "nameprefix" "could not set the public-access block on the shared bucket"
cmd "choudoufu apply -auto-approve   # in smoke-prod, then in smoke-prod-eu: same bucket, nothing else shared"
for estate in prod prod-eu; do
  ( cd "$SMOKE_WORK/$estate" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "nameprefix" "init failed in smoke-$estate"
  OUT="$(cd "$SMOKE_WORK/$estate" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "nameprefix" "apply failed in smoke-$estate: $OUT"
  grep -qE 'Resources: 1 added' <<< "$OUT" || fail "nameprefix" "smoke-$estate's record-backed resource did not apply: $OUT"
done
list_keys() { awsl s3api list-objects-v2 --bucket "$BUCKET" --prefix "$1" --query 'Contents[].Key' --output text | tr '\t' '\n' | grep -v '^None$' | sort; }
EU_BEFORE="$(list_keys "tofu-records/smoke-prod-eu/")"
[ -n "$EU_BEFORE" ] || fail "nameprefix" "smoke-prod-eu wrote nothing under its own prefix, so there is no neighbour to stay out of"
list_keys "tofu-" | evidence
proof "both estates' records are objects in one bucket, told apart by prefix alone."

step "2. the hazard, with no choudoufu in the loop"
explain \
  "The same two LIST calls, straight from the AWS CLI. With the trailing" \
  "slash the prefix names one estate. Without it, it names both."
cmd "aws s3api list-objects-v2 --prefix tofu-records/smoke-prod/    # delimited"
DELIMITED="$(list_keys "tofu-records/smoke-prod/")"
echo "$DELIMITED" | evidence
cmd "aws s3api list-objects-v2 --prefix tofu-records/smoke-prod     # bare"
BARE="$(list_keys "tofu-records/smoke-prod")"
echo "$BARE" | evidence
grep -q "smoke-prod-eu/" <<< "$DELIMITED" && fail "nameprefix" "the DELIMITED prefix returned smoke-prod-eu's keys; S3 is not matching the way this claim assumes"
grep -q "smoke-prod-eu/" <<< "$BARE" || fail "nameprefix" "the BARE prefix did not return smoke-prod-eu's keys, so this store does not have the hazard and the rest of the scenario proves nothing"
proof "the store itself will hand smoke-prod its neighbour's keys if asked without the slash. What choudoufu asks is the whole question."

# The binary whose requests get read. BREAK=1 swaps in one built with the
# delimiter dropped; nothing outside the binary can reach that line, so
# there is no way to manufacture this corruption in the cloud instead.
WIRE_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a binary with the delimiter dropped must be caught on the wire"
  explain \
    "The corruption: staterecord.NamespacePrefix, the one function every" \
    "key namespace goes through, stops appending its slash. It is applied" \
    "with go build -overlay, so the source tree is never touched, and it" \
    "needs this checkout and Go - a release binary cannot be broken."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "nameprefix" "BREAK=1 rebuilds choudoufu from this checkout with the delimiter dropped; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "nameprefix" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/live/staterecord/store.go"
  mkdir -p "$SMOKE_WORK/break"
  sed 's|return strings.TrimRight(prefix, "/") + "/"|return strings.TrimRight(prefix, "/")|' "$SRC" > "$SMOKE_WORK/break/store.go"
  cmp -s "$SRC" "$SMOKE_WORK/break/store.go" \
    && fail "nameprefix" "the break patch changed nothing in $SRC: NamespacePrefix no longer reads the way this arm expects, so the arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/store.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # NamespacePrefix without its slash"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "nameprefix" "the broken binary did not build"
  WIRE_BIN="$SMOKE_WORK/break/choudoufu"
fi

step "3. what smoke-prod asks the bucket for"
explain \
  "smoke-prod plans with the SDK's request log on. Every S3 request it" \
  "sends is in that log, so the claim is read off the wire rather than" \
  "inferred from a plan that happens to look right."
cmd "TF_LOG=debug choudoufu plan   # in smoke-prod"
WIRE="$SMOKE_WORKROOT/logs/nameprefix-prod-plan.log"
P_OUT="$(wire_log "$SMOKE_WORK/prod" "$WIRE_BIN" "$WIRE" plan -input=false -no-color 2>&1)" || true
[ -s "$WIRE" ] || fail "nameprefix" "no request log was written, so nothing was measured"
OWN_LISTS="$(grep -c 'rpc.method=ListObjectsV2' "$WIRE" || true)"
[ "$OWN_LISTS" -gt 0 ] \
  || fail "nameprefix" "the request log shows no ListObjectsV2 at all. Either the run never listed its records or the log format moved; a log with no neighbour in it proves nothing until the run's own requests are in it."
grep -oE '(GET|PUT|HEAD|DELETE) [^ ]*smoke-shared-records[^ ]*|prefix=tofu-records[^ &"]*' "$WIRE" | sort | uniq -c | sort -rn | head -8 | evidence
REACHED="$(grep -c 'smoke-prod-eu' "$WIRE" || true)"

if [ "${BREAK:-0}" = "1" ]; then
  [ "$REACHED" -gt 0 ] \
    || fail "nameprefix" "the binary with the delimiter dropped sent no request naming smoke-prod-eu. The wire assertion cannot see the defect it exists for, so its green in the ordinary run is scenery."
  grep -oE '[^ ]*smoke-prod-eu[^ &"]*' "$WIRE" | sort -u | head -3 | evidence
  proof "caught - without the slash, smoke-prod's run reached $REACHED request line(s) naming smoke-prod-eu. With it, none."
  exit 0
fi

[ "$REACHED" -eq 0 ] \
  || fail "nameprefix" "smoke-prod's run sent $REACHED request line(s) naming smoke-prod-eu: $(grep -oE '[^ ]*smoke-prod-eu[^ &"]*' "$WIRE" | sort -u | head -3 | tr '\n' ' ')"
BARE_LISTS="$(grep 'rpc.method=ListObjectsV2' "$WIRE" | grep -vc 'prefix=[^ &]*%2F\( \|&\|$\)' || true)"
[ "$BARE_LISTS" -eq 0 ] \
  || fail "nameprefix" "$BARE_LISTS of smoke-prod's LIST requests carry a prefix that does not end in a slash: $(grep 'rpc.method=ListObjectsV2' "$WIRE" | grep -oE 'prefix=[^ &]*' | sort -u | tr '\n' ' ')"
grep -q "No changes." <<< "$P_OUT" || fail "nameprefix" "smoke-prod's plan was not empty: $P_OUT"
proof "$OWN_LISTS LIST request(s), every one under a prefix ending in a slash, and not one request in the run names smoke-prod-eu."

step "4. smoke-prod tears itself down; its neighbour does not notice"
cmd "choudoufu apply -destroy -auto-approve   # in smoke-prod"
( cd "$SMOKE_WORK/prod" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "nameprefix" "smoke-prod's teardown failed"
EU_AFTER="$(list_keys "tofu-records/smoke-prod-eu/")"
[ "$EU_BEFORE" = "$EU_AFTER" ] \
  || fail "nameprefix" "smoke-prod-eu's keys changed across smoke-prod's teardown. before: $EU_BEFORE after: $EU_AFTER"
cmd "choudoufu plan   # in smoke-prod-eu"
EU_PLAN="$(cd "$SMOKE_WORK/prod-eu" && chdf plan -input=false -no-color 2>&1)" || fail "nameprefix" "smoke-prod-eu's replan failed: $EU_PLAN"
grep -E 'No changes\.' <<< "$EU_PLAN" | head -1 | evidence
grep -q "No changes." <<< "$EU_PLAN" || fail "nameprefix" "smoke-prod-eu's plan was not empty after its neighbour's teardown: $EU_PLAN"
proof "smoke-prod is gone, and every key smoke-prod-eu had is still there and still plans empty."

( cd "$SMOKE_WORK/prod-eu" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "nameprefix" "smoke-prod-eu's teardown failed"

echo "  What you watched: two estates sharing one bucket, the S3 API showing"
echo "  that a prefix without its slash names both of them, and one estate's"
echo "  whole run going by on the wire without a single request that names"
echo "  the other."
