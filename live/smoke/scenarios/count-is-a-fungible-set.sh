# CLAIMNAME-PENDING
# CLAIM 10 - A count pool is a fungible set: slot markers hold it together, so it scales down by removing one member and rebuilding nothing - and stripping the slots makes the run refuse rather than guess. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/count"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp "$ROOT/live/e2e/estate-block/versions.tf" "$SMOKE_WORK/"
cat > "$SMOKE_WORK/pool.tf" <<'TFEOF'
resource "aws_eip" "pool" {
  count  = 3
  domain = "vpc"
}
TFEOF
pool() { awsl ec2 describe-addresses \
  --query 'Addresses[?Tags[?Key==`tofu-estate`&&Value==`stateless-e2e-block`]].[AllocationId,Tags[?Key==`tofu-slot`]|[0].Value,Tags[?Key==`tofu-address`]|[0].Value]' \
  --output text | sort -k2; }
# settle waits until the tagging index the sweep uses reflects a manufactured
# tag change - floci's tagging API lags a raw create-tags/delete-tags, and a
# plan that raced it would read stale markers (the #756 lesson).
settle() { local want="$1" i; for i in $(seq 1 20); do
  if awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=stateless-e2e-block" \
    --query 'ResourceTagMappingList[].Tags' --output text 2>/dev/null | grep -q "$want"; then return 0; fi
  sleep 1; done; return 0; }

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "A count pool is a fungible SET. Its members are interchangeable -" \
  "the lint boundary forbids any argument from reading count.index, so" \
  "nothing about instance 2 distinguishes it from instance 0 - and the" \
  "positional index aws_eip.pool[1] is where a member sits, not what it" \
  "is. What it is, is a tofu-slot marker: a stable id minted once and" \
  "never reused. The slot is what holds the set together across a scale" \
  "change, so shrinking the pool removes one member and rebuilds" \
  "nothing, where stock renumbers and recreates the tail. Strip the" \
  "slots and the set has no name for its members - and the run refuses" \
  "rather than guess."

step "1. stand up a pool of three"
cmd "choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "count" "init failed"
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "count" "apply failed"
pool | evidence
[ "$(pool | grep -c .)" = "3" ] || fail "count" "expected three members"
[ "$(pool | awk '{print $2}' | sort -u | grep -c .)" = "3" ] || fail "count" "the three members do not carry three distinct slots"
proof "three interchangeable members, three distinct slots. The slot is the name; the index is just today's seat."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - strip one member's slot; the set loses its name"
  explain \
    "Delete the tofu-slot tag from one member. Now the set is half" \
    "slotted: two members answer 'which instance am I?' by slot, one has" \
    "no answer at all. That is two rules for one set, and the run must" \
    "REFUSE naming the disagreement rather than bind the odd member by a" \
    "guess. A clean plan here would mean the slot was never what bound" \
    "the set."
  VICTIM="$(pool | awk 'NR==2{print $1}')"
  awsl ec2 delete-tags --resources "$VICTIM" --tags Key=tofu-slot >/dev/null 2>&1 || fail "count" "BREAK: could not strip a slot"
  # settle: wait until the stripped object no longer shows a slot in the sweep's index
  for i in $(seq 1 20); do
    SLOTS_NOW="$(pool | awk '{print $2}' | grep -c '^[0-9]')"
    [ "$SLOTS_NOW" = "2" ] && break; sleep 1
  done
  BP="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  if ! grep -qiE "slot marker|disagree about slot" <<< "$BP"; then
    fail "count" "BREAK: the plan did not refuse the half-slotted set by name: $BP"
  fi
  grep -iE "disagree about slot|slot marker" <<< "$BP" | head -1 | evidence
  proof "caught - a set that carries slots on some members and not others has two answers, and the run stops. Slots are what bind the set."
  ( cd "$SMOKE_WORK" && sed -i '' 's/count  = 3/count  = 0/' pool.tf; chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "2. capture the survivor at the middle seat"
MID="$(pool | awk '$2=="1"{print $1}')"
[ -n "$MID" ] || fail "count" "no member holds slot 1"
echo "slot 1 lives on $MID" | evidence
proof "watch $MID - its seat is about to change and its identity must not."

step "3. scale to two - one removed, nothing rebuilt"
explain \
  "count drops to 2. The set matcher keeps the lowest slots and drops" \
  "the highest, which is the only rule that leaves every survivor on the" \
  "seat it already had. Expect exactly one destroy and zero creates - a" \
  "fungible shrink, not a renumber-and-rebuild."
cmd "count = 2 ; choudoufu plan ; choudoufu apply -auto-approve"
sed -i '' 's/count  = 3/count  = 2/' "$SMOKE_WORK/pool.tf"
P="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "count" "scale-down plan failed: $P"
D="$(grep -cE '^[[:space:]]*# .*will be destroyed' <<< "$P" || true)"
C="$(grep -cE '^[[:space:]]*# .*will be created' <<< "$P" || true)"
echo "plan: $D to destroy, $C to create" | evidence
[ "$D" = "1" ] || fail "count" "scale-down proposed $D destroys, not 1 - the pool renumbered: $P"
[ "$C" = "0" ] || fail "count" "scale-down proposed $C creates - a fungible shrink creates nothing: $P"
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "count" "scale-down apply failed"
proof "one destroy, zero creates. The set shrank by exactly one member."

step "4. the middle survivor is the same live object"
STILL="$(pool | awk -v a="$MID" '$1==a{print $1}')"
[ "$STILL" = "$MID" ] || fail "count" "$MID did not survive - it was rebuilt, the stock failure this claim excludes"
pool | evidence
proof "$MID is still here. Its seat moved and its identity did not - the whole difference between a slot and a subscript."

step "5. teardown"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "count" "teardown failed"
proof "the pool is gone."

echo "  What you watched: a count pool shrink by removing one member and"
echo "  keeping the rest as the exact same live objects, because a stable"
echo "  slot marker names each member of a fungible set. Stock renumbers"
echo "  and rebuilds the tail; here the tail does not exist."
