# record-is-the-values
# CLAIM 9 - The record is the values: a record-backed resource's values ARE its record, consulted on every default plan; mutate it and the plan sees it, corrupt it and the run refuses. ~1 min.

SMOKE_WORK="$SMOKE_WORKROOT/vouch"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cat > "$SMOKE_WORK/main.tf" <<'TFEOF'
terraform {
  live {
    estate = "smoke-vouch"
    record_store "local" { path = ".tofu-records" }
  }
}

resource "terraform_data" "effect" {
  input = "v1"
}
TFEOF
REC="$SMOKE_WORK/.tofu-records/tofu-records/smoke-vouch/terraform_data/dGVycmFmb3JtX2RhdGEuZWZmZWN0"

step "the claim"
explain \
  "The record is the values. A record-backed resource has no cloud" \
  "home: its values live in the record store. The store's answer is therefore" \
  "the authoritative read, and every DEFAULT plan consults it with" \
  "nothing opted into and no flag passed. That has two testable edges. An" \
  "out-of-band change to the record is a change to the values, so the" \
  "next plan must surface it. A record that cannot be read must refuse" \
  "the run by name, because improvising values would be planning" \
  "against fiction. Notice what this scenario does not need: Docker, an" \
  "emulator, credentials. The class this claim covers has no cloud, and" \
  "neither does its proof."

step "1. stand it up - one resource, one record"
cmd "choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "vouch" "init failed"
AOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "vouch" "apply failed: $AOUT"
grep -qE 'Resources: 1 added' <<< "$AOUT" || fail "vouch" "the resource did not apply: $AOUT"
[ -f "$REC" ] || fail "vouch" "no record file appeared at the expected key"
grep -o '"input":"v1"' "$REC" | head -1 | evidence
proof "the record on disk carries the resource's values, readable by anything that can read JSON."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a record that cannot answer must refuse, never improvise"
  explain \
    "The corruption: the record file is overwritten with garbage. A" \
    "run that shrugged and planned anyway would be planning against" \
    "made-up values - the plan might propose re-creating a live effect" \
    "or quietly dropping one. The run must refuse, and the refusal must" \
    "name the address whose record died."
  cmd "corrupt the record file ; choudoufu plan"
  echo 'this is not json {{{' > "$REC"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" && \
    fail "vouch" "the plan SUCCEEDED over a garbage record: $BOUT"
  grep -q "Cannot read a persisted record" <<< "$BOUT" \
    || fail "vouch" "the run failed but not with the record refusal: $BOUT"
  grep -q "terraform_data.effect" <<< "$BOUT" \
    || fail "vouch" "the refusal does not name the address whose record is unreadable: $BOUT"
  grep -E "Cannot read a persisted record" <<< "$BOUT" | head -1 | evidence
  proof "caught - an unreadable record refuses the run by name. The store never improvises."
  exit 0
fi

step "2. the default plan reads the record - and only the record"
cmd "choudoufu plan   # no flags, nothing opted into"
P1="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "vouch" "plan failed: $P1"
grep -q "No changes." <<< "$P1" || fail "vouch" "the estate did not plan clean: $P1"
proof "a default plan, converged from the store's own answer. Reading the estate and reading the records are the same act."

step "3. mutate the record out of band - the next default plan sees it"
explain \
  "Someone edits the record directly: input becomes v9-mutated behind" \
  "the tool's back. Because the record IS the values, that edit changed" \
  "the envelope - so the next DEFAULT plan must surface the difference" \
  "as a named reconvergence. No refresh flag, no cache trick - this is" \
  "what value attestation means."
cmd "edit the record file ; choudoufu plan"
python3 - "$REC" <<'PYEOF'
import json, sys
f = sys.argv[1]
d = json.load(open(f))
d = json.loads(json.dumps(d).replace('"v1"', '"v9-mutated"'))
json.dump(d, open(f, 'w'))
PYEOF
P2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "vouch" "post-mutation plan failed: $P2"
grep -q 'terraform_data.effect will be updated in-place' <<< "$P2" \
  || fail "vouch" "the mutated record did not surface as a named update: $P2"
grep -qE 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$P2" \
  || fail "vouch" "the plan proposes more than the one reconvergence: $P2"
grep -E '~ input' <<< "$P2" | head -1 | evidence
proof "the record moved and the plan saw it - on a default plan, because the record is not a cache of the values, it IS the values."

step "4. reconverge and tear down"
cmd "choudoufu apply -auto-approve ; choudoufu apply -destroy -auto-approve"
ROUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "vouch" "reconverge failed: $ROUT"
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$ROUT" || fail "vouch" "reconvergence was not one in-place change: $ROUT"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "vouch" "teardown failed"
proof "one apply put the config's value back; the destroy leaves nothing."

echo "  What you watched: a resource whose record is its values, planned from"
echo "  the store on every default run with nothing turned on; an out-of-band"
echo "  edit of that record surface as a named plan line; and the whole proof"
echo "  run without a cloud, an emulator, or a credential in sight. Where"
echo "  values live elsewhere - on cloud resources - the read stays the"
echo "  drift detector, and vouching serves only the explicit -refresh=false"
echo "  path (issue 692, measured 13 requests down to 5 on real AWS)."
