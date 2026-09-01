# staleness-costs-reads
# CLAIM 3 - Staleness costs reads, never results: any cache state yields the same plan; only the work differs. ~3 min.

SMOKE_WORK="$SMOKE_WORKROOT/staleness"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"
LOGDIR="$SMOKE_WORKROOT/stale-logs"; mkdir -p "$LOGDIR"

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
plan_filtered() { (cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 | grep -v '^discovering:'); }

step "the claim"
explain \
  "Staleness costs reads, never results. The state file here is a cache" \
  "of the projection, and the ruling it lives under has three lines: it" \
  "is never consulted for ownership, live wins any disagreement, and" \
  "losing it costs a slower run and nothing else. Stale is the EXPECTED" \
  "condition - the name of the project is fermented tofu. This scenario" \
  "makes the cache fresh, ancient, and absent, and demands one identical" \
  "answer each time; then it measures where the cost actually lands."

step "1. stand up, and manufacture a genuinely ancient cache"
explain \
  "First apply creates the estate and writes cache C1. Then the whole" \
  "estate is destroyed and applied AGAIN - a new world with new" \
  "server-assigned ids - while C1 is kept aside. C1 now remembers a" \
  "world that no longer exists: every VPC and subnet id in it is dead." \
  "That is not simulated staleness; it is the real thing."
cmd "apply ; save cache ; apply -destroy ; apply"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "stale" "init failed"
A1="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "stale" "first apply failed: $A1"
ADDED="$(grep -oE 'Resources: [0-9]+ added' <<< "$A1" | grep -oE '[0-9]+')"
[ -f "$CACHE" ] || fail "stale" "no cache after the first apply"
cp "$CACHE" "$SMOKE_WORK/ancient-cache.tfstate"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stale" "destroy failed"
A2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "stale" "second apply failed: $A2"
OLD_VPC="$(python3 -c "import json;d=json.load(open('$SMOKE_WORK/ancient-cache.tfstate'));print(next(i['attributes']['id'] for r in d['resources'] if r['type']=='aws_vpc' for i in r['instances']))" 2>/dev/null || echo '?')"
NEW_VPC="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-address,Values=aws_vpc.main" --query 'Vpcs[0].VpcId' --output text)"
echo "ancient cache remembers vpc $OLD_VPC; the live world is vpc $NEW_VPC" | evidence
[ "$OLD_VPC" != "$NEW_VPC" ] || fail "stale" "the recreate produced the same vpc id - the staleness is not real"
proof "the ancient cache and reality now disagree about every server-assigned id in the estate."

step "2. three cache states, one answer"
explain \
  "The same plan, three times: with the fresh cache, with the ancient" \
  "cache swapped in (dead ids and all), and with no cache file at all." \
  "The outputs must be byte-identical. A cache that could bend a plan" \
  "toward its own memory would be a record, and records are exactly what" \
  "this file is never allowed to be."
cmd "plan (fresh) ; plan (ancient) ; plan (absent)"
P_FRESH="$(plan_filtered)" || fail "stale" "fresh-cache plan failed"
grep -q "No changes." <<< "$P_FRESH" || fail "stale" "the healthy plan is not a no-op: $P_FRESH"
cp "$SMOKE_WORK/ancient-cache.tfstate" "$CACHE"
P_ANCIENT="$(plan_filtered)" || fail "stale" "ancient-cache plan failed"
rm -f "$CACHE"
P_ABSENT="$(plan_filtered)" || fail "stale" "absent-cache plan failed"
[ "$P_FRESH" = "$P_ANCIENT" ] || fail "stale" "the ancient cache changed the plan.
--- fresh ---
$P_FRESH
--- ancient ---
$P_ANCIENT"
[ "$P_FRESH" = "$P_ABSENT" ] || fail "stale" "the missing cache changed the plan.
--- fresh ---
$P_FRESH
--- absent ---
$P_ABSENT"
grep -E 'No changes\.' <<< "$P_ANCIENT" | head -1 | evidence
proof "fresh, ancient, and absent produced one identical answer. The cache's memory of dead ids was never consulted for anything a result depends on."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - move the world; the comparator must notice"
  explain \
    "Three identical outputs prove nothing if the comparison cannot" \
    "fail. Drift the live log group's retention away from the" \
    "configuration and compare again: the plans MUST differ now, or the" \
    "equality above was comparing blindfolded."
  cmd "aws logs put-retention-policy --retention-in-days 7 ; choudoufu plan"
  awsl logs put-retention-policy --log-group-name "/stateless-e2e-block/app" --retention-in-days 7 \
    || fail "stale" "BREAK: could not drift the retention"
  P_DRIFT="$(plan_filtered || true)"
  if [ "$P_FRESH" = "$P_DRIFT" ]; then
    fail "stale" "BREAK: the world moved and the plan output did not - the equality checks above are scenery"
  fi
  proof "caught: a real difference moves the comparator, so the three-way equality is a real check."
  exit 0
fi

step "3. the world moves and the fresh cache does not hide it"
explain \
  "Three days after the cache became the default, this exact shape" \
  "shipped broken: a fresh cache served a verified instance's attributes" \
  "and an out-of-band drift went invisible. The smoke caught it, the fix" \
  "gated cache-served reads behind -refresh=false, and this step is the" \
  "regression pinned forever: drift the retention while the cache is" \
  "fresh and present, and the default plan must show it."
cmd "aws logs put-retention-policy --retention-in-days 7 ; choudoufu plan"
( cd "$SMOKE_WORK" && chdf plan -input=false -no-color >/dev/null 2>&1 ) # rewrite nothing; ensure cache present from apply
[ -f "$CACHE" ] || ( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 )
awsl logs put-retention-policy --log-group-name "/stateless-e2e-block/app" --retention-in-days 7 \
  || fail "stale" "could not drift the retention"
P_DRIFT="$(plan_filtered)" || fail "stale" "the drift plan failed"
grep -E 'retention_in_days' <<< "$P_DRIFT" | head -2 | evidence
grep -qE '1 to change' <<< "$P_DRIFT" \
  || fail "stale" "the drift is not visible past a fresh cache - the #712 regression is back: $P_DRIFT"
AOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "stale" "the reconverging apply failed"
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$AOUT" \
  || fail "stale" "reconvergence was not exactly one in-place change: $AOUT"
proof "the read pass is drift detection, and no cache freshness excuses skipping it on a default plan. One apply reconverged it."

step "4. the one opt-in, and where the cost actually lives"
explain \
  "One path may serve results from the cache: -refresh=false, the same" \
  "flag with the same meaning as stock - the user asking, by name, for" \
  "stale. Even there this fork is stricter than stock: an instance is" \
  "served only if the estate sweep verified its marker moments before." \
  "Watch the debug stream say exactly which instances the cache supplied," \
  "then watch the same flag with no cache produce the identical answer."
cmd "plan -refresh=false (cache present, then absent) with TF_LOG capture"
R_WITH_OUT="$(cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$LOGDIR/with.log" "$TOFU" plan -refresh=false -input=false -no-color 2>&1 | grep -v '^discovering:')" \
  || fail "stale" "plan -refresh=false with cache failed"
rm -f "$CACHE"
R_WITHOUT_OUT="$(cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$LOGDIR/without.log" "$TOFU" plan -refresh=false -input=false -no-color 2>&1 | grep -v '^discovering:')" \
  || fail "stale" "plan -refresh=false without cache failed"
[ "$R_WITH_OUT" = "$R_WITHOUT_OUT" ] || fail "stale" "-refresh=false plans differ between cache present and absent.
--- with ---
$R_WITH_OUT
--- without ---
$R_WITHOUT_OUT"
HITS_WITH="$(grep -c 'state cache hit' "$LOGDIR/with.log" || true)"
HITS_WITHOUT="$(grep -c 'state cache hit' "$LOGDIR/without.log" || true)"
REQ_WITH=$(grep -c "HTTP Request Sent" "$LOGDIR/with.log" || true)
REQ_WITHOUT=$(grep -c "HTTP Request Sent" "$LOGDIR/without.log" || true)
grep "state cache supplied" "$LOGDIR/with.log" | sed 's/.*projection: //' | head -1 | evidence
echo "cache hits: $HITS_WITH with, $HITS_WITHOUT without; wire requests: $REQ_WITH with, $REQ_WITHOUT without" | evidence
[ "$HITS_WITH" -gt 0 ] || fail "stale" "-refresh=false with a fresh, sweep-verified cache served nothing - the opt-in path is dead"
[ "$HITS_WITHOUT" -eq 0 ] || fail "stale" "cache hits were reported with no cache file present - the counter is lying"
proof "the opt-in path served $HITS_WITH instance(s) and losing the cache changed only work, never the answer. Honest footnote: on this small estate the wire saving rounds to nothing ($REQ_WITH vs $REQ_WITHOUT requests) because the sweep currently vouches a narrow slice and other phases still read - widening that slice is tracked, open work (#692). The claim never promised big savings; it promised the price of staleness is only ever paid in work."

step "5. the same answer where values live in the record store"
explain \
  "The cache also memorizes record-backed resources - ones whose values" \
  "live in the live backend's record store, not AWS. Same manufacture," \
  "harder shape: save the cache, destroy the estate, rebuild it SMALLER." \
  "The saved cache now holds dead identities AND a phantom resource that" \
  "exists nowhere. If the cache had any authority the phantom would leak" \
  "into the plan. The store's List is the inventory; the answer must not" \
  "move."
R="$SMOKE_WORKROOT/stale-records"; mkdir -p "$R"
RCACHE="$R/.terraform/choudoufu-cache.tfstate"
rplan() { (cd "$R" && chdf plan -input=false -no-color 2>&1 | grep -v '^discovering:'); }
cat > "$R/main.tf" <<'TFEOF'
terraform {
  live {
    estate = "smoke-stale-records"
  }
}

resource "terraform_data" "survivor" {
  input = "s"
}

resource "terraform_data" "phantom" {
  input = "p"
}
TFEOF
cmd "apply ; save cache ; destroy ; drop a block ; apply ; plan (fresh vs ancient)"
( cd "$R" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "stale" "record init failed"
( cd "$R" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stale" "record apply failed"
[ -f "$RCACHE" ] || fail "stale" "no cache after the record apply"
cp "$RCACHE" "$R/ancient-cache.tfstate" || fail "stale" "saving the record cache failed"
( cd "$R" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stale" "record destroy failed"
cat > "$R/main.tf" <<'TFEOF'
terraform {
  live {
    estate = "smoke-stale-records"
  }
}

resource "terraform_data" "survivor" {
  input = "s"
}
TFEOF
( cd "$R" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "stale" "record re-apply failed"
RP_FRESH="$(rplan)" || fail "stale" "fresh record plan failed"
grep -q "No changes." <<< "$RP_FRESH" || fail "stale" "the rebuilt record estate is not converged: $RP_FRESH"
cp "$R/ancient-cache.tfstate" "$RCACHE" || fail "stale" "restoring the ancient record cache failed"
RP_ANCIENT="$(rplan)" || fail "stale" "ancient-cache record plan failed"
[ "$RP_FRESH" = "$RP_ANCIENT" ] \
  || fail "stale" "the ancient record cache bent the plan: fresh [$RP_FRESH] vs ancient [$RP_ANCIENT]"
if grep -q 'phantom' <<< "$RP_ANCIENT"; then
  fail "stale" "the phantom leaked out of the ancient cache into the plan: $RP_ANCIENT"
fi
echo "cache remembers 2 resources including terraform_data.phantom; plan through it: $(grep -m1 'No changes.' <<< "$RP_ANCIENT")" | evidence
proof "a cache holding a resource that exists nowhere changed nothing - not even a destroy for the phantom. The store's List is the inventory; the cache is a memo."
( cd "$R" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "stale" "record estate teardown failed"

step "6. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "stale" "teardown failed: $DOUT"
grep -qE "Resources: 0 added, 0 changed, $ADDED destroyed" <<< "$DOUT" \
  || fail "stale" "teardown did not remove exactly $ADDED resources: $DOUT"
proof "$ADDED destroyed - the estate is gone."

echo "  What you watched: a cache holding dead ids, an empty cache, and a"
echo "  fresh one produce byte-identical plans - against the cloud and"
echo "  against the record store, phantom resource and all; a moving world stay visible"
echo "  straight through a fresh cache; and the only price of losing the"
echo "  cache appear exactly where it belongs - in the request count of the"
echo "  one path that opts into staleness by name."
