# unchanged-is-free
# CLAIM 9 - Unchanged is free: re-planning what did not change costs no reads where a vouch stands in, the estate can refuse with one argument, and for record-backed resources the record itself is the attestation. ~3 min.

SMOKE_WORK="$SMOKE_WORKROOT/unchanged"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"
RECDIR="$SMOKE_WORKROOT/unchanged-records"
mkdir -p "$RECDIR"
cat > "$RECDIR/main.tf" <<'TFEOF'
terraform {
  live {
    estate = "smoke-unchanged"
    record_store "local" { path = ".tofu-records" }
  }
}

resource "terraform_data" "effect" {
  input = "v1"
}
TFEOF
REC="$RECDIR/.tofu-records/tofu-records/smoke-unchanged/terraform_data/dGVycmFmb3JtX2RhdGEuZWZmZWN0"
LOGDIR="$SMOKE_WORKROOT/unchanged-logs"; mkdir -p "$LOGDIR"

step "the claim"
explain \
  "Unchanged is free. Re-planning an estate that did not change should" \
  "not cost a full re-read of it, and here it does not. On the" \
  "-refresh=false path an instance the run can vouch for is served" \
  "from the cache; its wire reads are never made. The whole pass" \
  "answers to one estate-level argument - reads = \"full\" turns it off," \
  "and CHOUDOUFU_READS overrides per run - because a default this" \
  "consequential needs an off switch you can point at. And for" \
  "record-backed resources the attestation is the record itself, on" \
  "every plan, with nothing opted into at all."

if [ "${BREAK:-0}" != "1" ]; then
step "1. stand the estate up"
cmd "choudoufu apply -auto-approve"
stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "unchanged" "init failed"
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "unchanged" "apply failed"
proof "an estate up, a fresh cache beside it, nothing changed since."

step "2. the free re-plan, and the argument that refuses it"
explain \
  "The same -refresh=false plan twice: once under the default policy" \
  "(reads selective), once with CHOUDOUFU_READS=full - the same off" \
  "switch the live block's reads argument sets estate-wide. Selective" \
  "must serve vouched instances and skip their reads outright; full" \
  "must serve nothing and pay every read. The outputs must not differ" \
  "by a byte - the toggle prices the plan, never changes it."
cmd "plan -refresh=false ; CHOUDOUFU_READS=full plan -refresh=false"
S_OUT="$(cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$LOGDIR/sel.log" "$TOFU" plan -refresh=false -input=false -no-color 2>&1 | grep -v '^discovering:')" \
  || fail "unchanged" "the selective plan failed"
F_OUT="$(cd "$SMOKE_WORK" && CHOUDOUFU_READS=full TF_LOG=debug TF_LOG_PATH="$LOGDIR/full.log" "$TOFU" plan -refresh=false -input=false -no-color 2>&1 | grep -v '^discovering:')" \
  || fail "unchanged" "the full plan failed"
HITS_SEL=$(grep -c 'state cache hit' "$LOGDIR/sel.log" || true)
HITS_FULL=$(grep -c 'state cache hit' "$LOGDIR/full.log" || true)
REQ_SEL=$(grep -c "HTTP Request Sent" "$LOGDIR/sel.log" || true)
REQ_FULL=$(grep -c "HTTP Request Sent" "$LOGDIR/full.log" || true)
[ "$HITS_SEL" -gt 0 ] || fail "unchanged" "the selective pass served nothing - unchanged was not free"
[ "$HITS_FULL" -eq 0 ] || fail "unchanged" "reads=full still served $HITS_FULL from the cache - the off switch does not switch off"
[ "$REQ_SEL" -lt "$REQ_FULL" ] || fail "unchanged" "the selective pass saved no requests ($REQ_SEL vs $REQ_FULL)"
[ "$S_OUT" = "$F_OUT" ] || fail "unchanged" "the toggle changed the plan output - it may only ever change the price: [$S_OUT] vs [$F_OUT]"
echo "selective: $HITS_SEL served, $REQ_SEL requests; full: $HITS_FULL served, $REQ_FULL requests; outputs identical" | evidence
proof "unchanged cost $REQ_SEL requests instead of $REQ_FULL, and the one-argument off switch restored the full bill without moving the answer."

step "3. teardown the cloud estate"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "unchanged" "teardown failed"
proof "gone."
fi

step "4. the record-backed half - the record is the attestation"
explain \
  "A record-backed resource has no cloud home: its values live in the" \
  "record store, and unchanged is attested by the record itself on" \
  "every DEFAULT plan. No flag or cache is involved. Two edges" \
  "prove it. An out-of-band edit of the record is a change to the" \
  "values, so the next plan must surface it. A record that cannot be" \
  "read must refuse the run by name, because improvising values would" \
  "be planning against fiction."
cmd "choudoufu apply ; edit the record ; choudoufu plan"
( cd "$RECDIR" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "unchanged" "record init failed"
ROUT="$(cd "$RECDIR" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "unchanged" "record apply failed: $ROUT"
[ -f "$REC" ] || fail "unchanged" "no record file appeared at the expected key"
if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control: the record file is overwritten with garbage. A run" \
    "that shrugged and planned anyway would be planning against made-up" \
    "values. It must refuse, naming the address whose record died."
  echo 'this is not json {{{' > "$REC"
  BOUT="$(cd "$RECDIR" && chdf plan -input=false -no-color 2>&1)" && \
    fail "unchanged" "the plan SUCCEEDED over a garbage record: $BOUT"
  grep -q "Cannot read a persisted record" <<< "$BOUT" \
    || fail "unchanged" "the run failed but not with the record refusal: $BOUT"
  grep -q "terraform_data.effect" <<< "$BOUT" \
    || fail "unchanged" "the refusal does not name the address: $BOUT"
  grep -E "Cannot read a persisted record" <<< "$BOUT" | head -1 | evidence
  proof "caught - an unreadable record refuses the run by name. The store never improvises."
  exit 0
fi
P1="$(cd "$RECDIR" && chdf plan -input=false -no-color 2>&1)" || fail "unchanged" "record plan failed: $P1"
grep -q "No changes." <<< "$P1" || fail "unchanged" "the record estate did not plan clean: $P1"
python3 - "$REC" <<'PYEOF'
import json, sys
f = sys.argv[1]
d = json.load(open(f))
d = json.loads(json.dumps(d).replace('"v1"', '"v9-mutated"'))
json.dump(d, open(f, 'w'))
PYEOF
P2="$(cd "$RECDIR" && chdf plan -input=false -no-color 2>&1)" || fail "unchanged" "post-mutation plan failed: $P2"
grep -q 'terraform_data.effect will be updated in-place' <<< "$P2" \
  || fail "unchanged" "the mutated record did not surface as a named update: $P2"
grep -qE 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$P2" \
  || fail "unchanged" "the plan proposes more than the one reconvergence: $P2"
grep -E '~ input' <<< "$P2" | head -1 | evidence
( cd "$RECDIR" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "unchanged" "reconverge failed"
( cd "$RECDIR" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "unchanged" "record teardown failed"
proof "the record moved and the plan saw it - unchanged means the record says so, and this one stopped saying so."

echo "  What you watched: a re-plan of an unchanged estate served from the"
echo "  cache with its reads never made and the bill measurably lower; one"
echo "  argument refuse the whole deal without moving a byte of the answer;"
echo "  and a record-backed resource treat its record as the attestation -"
echo "  edit the record and the plan sees it, corrupt it and the run refuses."
