# a-waiver-names-what-it-waives
# CLAIM 30 - A bucket waiver waives only the assertion it names, and says so on every run. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/waiver"
BUCKET="smoke-waived-records"
mkdir -p "$SMOKE_WORK/est" "$SMOKE_WORK/typo"; export SMOKE_WORK

write_estate() { # dir estate input allow_insecure-literal
  cat > "$1/main.tf" <<TFEOF
terraform {
  live {
    estate = "$2"

    record_store "s3" {
      bucket         = "$BUCKET"
      allow_insecure = $4
    }
  }
}

resource "terraform_data" "effect" {
  input = "$3"
}
TFEOF
}
write_estate "$SMOKE_WORK/est" smoke-waived v1 '["versioning"]'
write_estate "$SMOKE_WORK/typo" smoke-waived-typo v1 '["versionning"]'

EXPIRING='{"Rules":[{"ID":"expire-noncurrent","Status":"Enabled","Filter":{"Prefix":""},"NoncurrentVersionExpiration":{"NoncurrentDays":30}}]}'
PAB='BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true'
WARNING='versioning assertion is waived'
COST='cannot be brought back'

# warned <label> <output>: the run proceeded, and it named the waived setting
# and what was given up.
# flat undoes the CLI's word wrap, which breaks a diagnostic's sentences
# across lines (and prefixes them with a box-drawing bar) wherever the
# terminal width falls.
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }
warned() {
  local text; text="$(flat <<< "$2")"
  grep -q "$WARNING" <<< "$text" || fail "waiver" "[$1] the run said nothing about the versioning waiver it is running under: $2"
  grep -q "$COST" <<< "$text" || fail "waiver" "[$1] the warning names the waiver but not what it costs: $2"
}

step "the claim"
explain \
  "A bucket's three assertions can be waived, for a bucket an operator" \
  "has reason to run differently or a role that cannot read the bucket's" \
  "configuration. The shape of that waiver is the point. It is a list of" \
  "names, not a flag, so waiving one assertion leaves the other two in" \
  "force and the configuration records which risk was taken. And it is" \
  "loud on every run: a waiver that goes quiet after the first apply" \
  "looks exactly like a bucket that passes."

# The binary under test. BREAK=1 swaps in one whose waiver warning goes
# quiet once the estate has run before - the defect the design is shaped
# against, and one no single-run check can see.
RUN_BIN="$TOFU"
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a waiver that goes quiet on the second run must be caught"
  explain \
    "The corruption is in the binary, so it is built with go build" \
    "-overlay and the source tree is never touched: the warning is" \
    "emitted only while the estate has no state cache yet, which is to" \
    "say on its first run and never again."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "waiver" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "waiver" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/command/live_mode.go"
  mkdir -p "$SMOKE_WORK/break"
  sed 's|if !r.waiverWarned {|if _, quietErr := os.Stat(".terraform/choudoufu-cache.tfstate"); !r.waiverWarned \&\& quietErr != nil {|' "$SRC" > "$SMOKE_WORK/break/live_mode.go"
  cmp -s "$SRC" "$SMOKE_WORK/break/live_mode.go" \
    && fail "waiver" "the break patch changed nothing in $SRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/live_mode.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # the warning, first run only"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "waiver" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi
run() { ( cd "$1" && shift && "$RUN_BIN" "$@" ); }

step "1. a bucket with no versioning, and a waiver that names versioning"
stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
awsl s3api create-bucket --bucket "$BUCKET" >/dev/null || fail "waiver" "could not create the bucket"
awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration "$EXPIRING" >/dev/null || fail "waiver" "could not set the lifecycle"
awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration "$PAB" >/dev/null || fail "waiver" "could not set the public-access block"
run "$SMOKE_WORK/est" init -input=false -no-color >/dev/null 2>&1 || fail "waiver" "init failed"
cmd "choudoufu apply -auto-approve   # allow_insecure = [\"versioning\"]"
OUT1="$(run "$SMOKE_WORK/est" apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "waiver" "the apply was refused although the one failing assertion is the one waived: $OUT1"
grep -qE 'Resources: 1 added' <<< "$OUT1" || fail "waiver" "nothing applied: $OUT1"
warned "run 1" "$OUT1"
grep -q "would have refused this apply" <<< "$(flat <<< "$OUT1")" \
  || fail "waiver" "the apply read the bucket and did not say the waiver is hiding a real failure: $OUT1"
grep -E "$WARNING|would have refused|versioning has never" <<< "$OUT1" | head -3 | evidence
proof "the run went ahead, named the assertion it skipped and what that costs, and said the bucket really does fail it."

step "2. loud on every run"
explain \
  "The same estate again: a plan, then a second apply. Nothing about the" \
  "bucket or the configuration has changed, so if the warning were tied" \
  "to the first run, or to something having changed, this is where it" \
  "would go quiet."
cmd "choudoufu plan"
OUT2="$(run "$SMOKE_WORK/est" plan -input=false -no-color 2>&1)" || fail "waiver" "the plan failed: $OUT2"
if [ "${BREAK:-0}" = "1" ]; then
  if grep -q "$WARNING" <<< "$OUT2"; then
    fail "waiver" "the binary built to go quiet on run two still warned, so the break did not take and this control proves nothing"
  fi
  grep -E 'No changes|Plan:' <<< "$OUT2" | head -1 | evidence
  proof "caught - run two proceeded under the waiver without a word. The ordinary run's second-run check is what stands between that and a green."
  exit 0
fi
warned "run 2, a plan" "$OUT2"
write_estate "$SMOKE_WORK/est" smoke-waived v2 '["versioning"]'
cmd "choudoufu apply -auto-approve   # a second apply"
OUT3="$(run "$SMOKE_WORK/est" apply -auto-approve -input=false -no-color 2>&1)" || fail "waiver" "the second apply failed: $OUT3"
warned "run 3, a second apply" "$OUT3"
count_warnings() { flat <<< "$1" | grep -o "$WARNING" | wc -l | tr -d ' '; }
echo "run 1: $(count_warnings "$OUT1")  run 2: $(count_warnings "$OUT2")  run 3: $(count_warnings "$OUT3")   (warnings naming the versioning waiver)" | evidence
proof "three runs, three warnings. A plan warns too, from the configuration alone, without reading the bucket."

step "3. the other two assertions are still in force"
explain \
  "Versioning is waived. The lifecycle and the public-access block are" \
  "not, and each is now broken in turn."
for arm in lifecycle public_access_block; do
  if [ "$arm" = lifecycle ]; then
    awsl s3api delete-bucket-lifecycle --bucket "$BUCKET" >/dev/null
  else
    awsl s3api delete-public-access-block --bucket "$BUCKET" >/dev/null
  fi
  write_estate "$SMOKE_WORK/est" smoke-waived "arm-$arm" '["versioning"]'
  cmd "choudoufu apply -auto-approve   # $arm broken, versioning still waived"
  A_OUT="$(run "$SMOKE_WORK/est" apply -auto-approve -input=false -no-color 2>&1)" \
    && fail "waiver" "[$arm] a waiver that names only versioning let an apply through with $arm broken: $A_OUT"
  grep -q "fails its $arm assertion" <<< "$(flat <<< "$A_OUT")" || fail "waiver" "[$arm] the run failed without naming the $arm assertion: $A_OUT"
  grep -q "fails its versioning assertion" <<< "$(flat <<< "$A_OUT")" && fail "waiver" "[$arm] versioning was refused although it is waived: $A_OUT"
  grep -E "fails its $arm assertion" <<< "$A_OUT" | head -1 | evidence
  awsl s3api put-bucket-lifecycle-configuration --bucket "$BUCKET" --lifecycle-configuration "$EXPIRING" >/dev/null
  awsl s3api put-public-access-block --bucket "$BUCKET" --public-access-block-configuration "$PAB" >/dev/null
done
proof "each refused by its own name, and versioning, which is waived, never among them."

step "4. a typo is refused, not ignored"
explain \
  "allow_insecure = [\"versionning\"] waives nothing. Accepted silently it" \
  "would still read as a waiver to whoever reviews the configuration."
cmd "choudoufu plan   # allow_insecure = [\"versionning\"]"
T_OUT="$(run "$SMOKE_WORK/typo" init -input=false -no-color 2>&1; run "$SMOKE_WORK/typo" plan -input=false -no-color 2>&1)" \
  && fail "waiver" "a misspelt waiver was accepted: $T_OUT"
grep -q 'versionning' <<< "$T_OUT" || fail "waiver" "the refusal does not name the misspelt setting: $T_OUT"
grep -E 'versionning' <<< "$T_OUT" | head -1 | evidence
proof "refused at configuration load, naming the word it did not recognize and listing the three it does."

step "5. teardown"
write_estate "$SMOKE_WORK/est" smoke-waived v2 '["versioning"]'
run "$SMOKE_WORK/est" apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 || fail "waiver" "teardown failed"
proof "gone."

echo "  What you watched: a waiver let one named assertion go, said so with"
echo "  its cost on a first apply, a plan and a second apply alike, left the"
echo "  other two assertions refusing by name, and would not accept a name"
echo "  it did not know."
