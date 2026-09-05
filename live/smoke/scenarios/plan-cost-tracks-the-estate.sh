# plan-cost-tracks-the-estate
# CLAIM 14 - A plan costs its estate, not its account: one estate of a many-estate terralith plans as if it were alone, however large the rest of the account grows. ~2 min.

WORK="$SMOKE_WORKROOT/plancost"
mkdir -p "$WORK/net" "$WORK/data"
# The compose file interpolates the oracle service, so SMOKE_WORK must be set
# even though this scenario never uses the oracle leg.
SMOKE_WORK="$WORK"; export SMOKE_WORK
prov() { printf 'provider "aws" {\n  skip_credentials_validation = true\n  skip_requesting_account_id  = true\n  skip_metadata_api_check     = true\n}\n'; }
cat > "$WORK/net/main.tf" <<TFEOF
terraform {
  required_providers { aws = { source = "hashicorp/aws", version = "= 6.58.0" } }
  live { estate = "demo-net" }
}
$(prov)
resource "aws_vpc" "main" { cidr_block = "10.20.0.0/16" }
resource "aws_subnet" "a" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.20.1.0/24"
}
resource "aws_subnet" "b" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.20.2.0/24"
}
resource "aws_security_group" "sg" {
  name        = "demo-net-sg"
  description = "the network slice"
  vpc_id      = aws_vpc.main.id
}
TFEOF
cat > "$WORK/data/main.tf" <<TFEOF
terraform {
  required_providers { aws = { source = "hashicorp/aws", version = "= 6.58.0" } }
  live { estate = "demo-data" }
}
$(prov)
resource "aws_cloudwatch_log_group" "g" {
  count = 8
  name  = "/demo-data/g\${count.index}"
}
TFEOF

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
LOGS="$SMOKE_WORKROOT/plancost-logs"; mkdir -p "$LOGS"
planplan() { # $1=dir $2=logname $3+=extra flags
  local dir="$1" name="$2"; shift 2
  ( cd "$WORK/$dir" && TF_LOG=debug TF_LOG_PATH="$LOGS/$name.log" "$TOFU" plan -input=false -no-color "$@" >/dev/null 2>&1 || true )
  grep -c "HTTP Request Sent" "$LOGS/$name.log" 2>/dev/null || echo 0
}

step "the claim"
explain \
  "A plan's cost tracks its estate, not the account. In choudoufu the" \
  "whole account is the terralith - every resource lives in it, sorted" \
  "into estates by tag - and there is no bound state file that a plan" \
  "must read end to end. So planning one estate reads only that estate's" \
  "resources, and it stays that cheap no matter how many other estates" \
  "share the account. Stamp up a monolith, carve it into estates, and" \
  "each one plans as if it were alone. Cost is measured in API calls," \
  "the work that actually scales."

step "1. stand up one estate, and plan it alone"
cmd "choudoufu apply (demo-net: 4 resources) ; choudoufu plan"
( cd "$WORK/net" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "plancost" "net init failed"
( cd "$WORK/net" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "plancost" "net apply failed"
NET_ALONE="$(planplan net net-alone)"
[ "$NET_ALONE" -gt 0 ] 2>/dev/null || fail "plancost" "the net plan made no measurable calls: $NET_ALONE"
echo "demo-net plan, account holds only demo-net: $NET_ALONE calls" | evidence
proof "$NET_ALONE calls to plan the four-resource estate."

step "2. grow the account with another estate, and replan the first"
explain \
  "A second estate joins the account: demo-data, eight more resources," \
  "owned by a different tag. The account - the terralith - is now" \
  "larger. Replan demo-net, unchanged. If cost tracked the account, this" \
  "number would climb. If it tracks the estate, it does not move."
cmd "choudoufu apply (demo-data: 8 resources) ; choudoufu plan (demo-net again)"
( cd "$WORK/data" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "plancost" "data init failed"
( cd "$WORK/data" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "plancost" "data apply failed"
NET_WITHDATA="$(planplan net net-withdata)"
echo "demo-net plan, account now holds demo-net AND demo-data: $NET_WITHDATA calls" | evidence
[ "$NET_WITHDATA" = "$NET_ALONE" ] \
  || fail "plancost" "the net plan cost moved when another estate joined the account ($NET_ALONE -> $NET_WITHDATA) - cost is not estate-scoped"
proof "$NET_WITHDATA calls, unchanged. Eight resources in another estate cost the demo-net plan nothing."

step "3. what reading the whole terralith would cost"
explain \
  "For scale, ask what it costs to scan the whole account instead of one" \
  "estate - the account-wide question -adoption-only answers, and the" \
  "shape a bound state file forces on every plan. On this tiny account it" \
  "is already an order of magnitude more, and the ratio only grows as the" \
  "terralith does."
cmd "choudoufu plan -adoption-only (demo-net dir, account-wide)"
ACCOUNT_WIDE="$(planplan net account-wide -adoption-only)"
echo "scoped to demo-net: $NET_WITHDATA calls   ·   account-wide scan: $ACCOUNT_WIDE calls" | evidence
[ "$ACCOUNT_WIDE" -gt "$NET_WITHDATA" ] 2>/dev/null \
  || fail "plancost" "the account-wide scan was not more expensive than the scoped plan ($ACCOUNT_WIDE vs $NET_WITHDATA) - the scoping saved nothing"
RATIO=$(( ACCOUNT_WIDE / (NET_WITHDATA>0?NET_WITHDATA:1) ))
proof "scoping to the estate turned a ${ACCOUNT_WIDE}-call account scan into ${NET_WITHDATA} calls, about ${RATIO}x less on this small account."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - defeat the scoping; the cost must explode"
  explain \
    "The claim rests entirely on the estate scoping being what keeps the" \
    "plan cheap. Defeat it: run the same estate account-wide. If the cost" \
    "does NOT jump to the whole-account scan, then the scoped number was" \
    "cheap for some other reason and the claim proves nothing. The plan is" \
    "only allowed to be cheap because it is scoped."
  cmd "choudoufu plan -adoption-only   # scoping defeated"
  BROKEN="$(planplan net break-wide -adoption-only)"
  echo "scoped: $NET_WITHDATA calls   ·   scoping defeated: $BROKEN calls" | evidence
  if [ "$BROKEN" -le $(( NET_WITHDATA * 3 )) ] 2>/dev/null; then
    fail "plancost" "defeating the scoping did NOT explode the cost ($NET_WITHDATA -> $BROKEN) - the scoping was not what made the plan cheap"
  fi
  proof "caught - without the estate scoping the same plan costs $BROKEN calls, not $NET_WITHDATA. The scoping is the whole saving."
  exit 0
fi

step "4. teardown"
cmd "choudoufu apply -destroy (both estates)"
( cd "$WORK/net" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "plancost" "net teardown failed"
( cd "$WORK/data" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "plancost" "data teardown failed"
proof "both estates gone."

echo "  What you watched: one estate of a two-estate account planned for the"
echo "  same $NET_ALONE calls whether or not the other estate existed, while"
echo "  scanning the whole account cost $ACCOUNT_WIDE. A plan's cost tracks its"
echo "  estate, so a terralith stamped into estates plans one slice at a time,"
echo "  each as cheap as if it stood alone."
