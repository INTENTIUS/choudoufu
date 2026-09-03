# CLAIMNAME-PENDING
# CLAIM 10 - Count instances stay distinguishable: a fungible pool scales down by slot with nothing rebuilt, and the tool refuses any config where an index expression would collapse two instances onto one identity. ~2 min.

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

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "Count instances stay distinguishable. A count block is a fungible" \
  "SET, not a list: its members are interchangeable, and the position" \
  "aws_eip.pool[1] is a seat, not a name. A server-assigned member is" \
  "named by a tofu-slot marker - a stable id minted once, never reused -" \
  "so the pool scales down by dropping a slot and rebuilds nothing," \
  "where stock renumbers and recreates the tail. And where an index" \
  "WOULD feed identity, the tool refuses any expression that could" \
  "collapse two instances onto one - the boundary that keeps a set a" \
  "set."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - an index expression that collides two instances must be refused"
  explain \
    "The guarantee under the whole claim: a count instance's identity may" \
    "never depend on its index in a way that two instances could share." \
    "Add a client-named pool whose name is smoke-pool-\${count.index % 2}" \
    "- at count 3 that renders smoke-pool-0 for BOTH index 0 and index 2." \
    "Two instances, one identity: the set has stopped being a set. The" \
    "run must refuse, naming count.index, before anything is created. A" \
    "clean plan here would mean the distinguishability the claim rests on" \
    "is not actually enforced."
  cat > "$SMOKE_WORK/collide.tf" <<'TFEOF'
resource "aws_iam_role" "collide" {
  count              = 3
  name               = "smoke-pool-${count.index % 2}"
  assume_role_policy = jsonencode({ Version = "2012-10-17", Statement = [] })
}
TFEOF
cmd "add a count whose name is smoke-pool-\${count.index % 2} ; choudoufu plan"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "count" "BREAK init failed"
BP="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
if ! grep -qiE "count.index is not available|count.index in aws_iam_role" <<< "$BP"; then
  fail "count" "BREAK: a colliding index expression was not refused: $BP"
fi
if grep -qE 'will be created' <<< "$BP"; then
  fail "count" "BREAK: the plan proposed creates despite the collision - it did not refuse before acting: $BP"
fi
grep -iE "count.index" <<< "$BP" | head -2 | evidence
proof "caught - an index that renders one identity for two instances is refused by name, before a create. Distinguishability is enforced, not assumed."
  exit 0
fi

step "1. stand up a fungible pool of three"
cmd "choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "count" "init failed"
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "count" "apply failed"
pool | evidence
[ "$(pool | grep -c .)" = "3" ] || fail "count" "expected three members"
[ "$(pool | awk '{print $2}' | sort -u | grep -c .)" = "3" ] || fail "count" "the three members do not carry three distinct slots"
proof "three interchangeable members, three distinct slots. The slot is the name; the seat is just where it sits today."

step "2. capture the survivor at the middle seat"
MID="$(pool | awk '$2=="1"{print $1}')"
[ -n "$MID" ] || fail "count" "no member holds slot 1"
echo "slot 1 lives on $MID" | evidence
proof "watch $MID - its seat is about to change and its identity must not."

step "3. scale to two - one removed, nothing rebuilt"
explain \
  "count drops to 2. The matcher keeps the lowest slots and drops the" \
  "highest, the only rule that leaves every survivor on the seat it had." \
  "Expect exactly one destroy and zero creates - a fungible shrink."
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

echo "  What you watched: a fungible pool shrink by removing one member and"
echo "  keeping the rest as the exact same live objects, and the tool refuse"
echo "  a config where an index expression would collapse two instances onto"
echo "  one identity. A count stays a set because its members always stay"
echo "  distinguishable."
