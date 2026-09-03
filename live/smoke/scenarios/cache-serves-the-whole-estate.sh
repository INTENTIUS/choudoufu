# cache-serves-the-whole-estate
# CLAIM 10 - The cache serves the whole estate: on -refresh=false every converged instance is served from cache, needs-discovery resources included, so one estate of a terralith plans without re-reading the cloud - while a default plan still refreshes and a deletion is still caught. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/refserve"; export SMOKE_WORK
mkdir -p "$SMOKE_WORK"
cat > "$SMOKE_WORK/main.tf" <<'TFEOF'
terraform {
  required_providers { aws = { source = "hashicorp/aws", version = "= 6.58.0" } }
  live { estate = "refserve" }
}
provider "aws" {
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
}
resource "aws_vpc" "main" { cidr_block = "10.40.0.0/16" }
resource "aws_subnet" "a" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.40.1.0/24"
}
resource "aws_subnet" "b" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.40.2.0/24"
}
resource "aws_security_group" "sg" {
  name        = "refserve-sg"
  description = "the network estate"
  vpc_id      = aws_vpc.main.id
}
TFEOF
LOGS="$SMOKE_WORKROOT/refserve-logs"; mkdir -p "$LOGS"
countplan() { # $1=logname $2+=flags
  local name="$1"; shift
  ( cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$LOGS/$name.log" "$TOFU" plan -input=false -no-color "$@" >/dev/null 2>&1 || true )
}
calls() { grep -c "HTTP Request Sent" "$LOGS/$1.log" 2>/dev/null || true; }
hits() { grep -c "state cache hit" "$LOGS/$1.log" 2>/dev/null || true; }

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "On the -refresh=false path a converged estate is served from its" \
  "cache, and needs-discovery resources - the VPCs, subnets and security" \
  "groups a real estate is mostly made of, whose ids the cloud assigns -" \
  "are served like any other. That is what lets one estate of a" \
  "decomposed terralith plan at the speed of reading a file rather than" \
  "re-reading the cloud. The cache is non-authoritative throughout, so" \
  "drift is still caught: a resource changed out of band is read, never" \
  "served stale."

step "1. stand the estate up - four needs-discovery resources"
cmd "choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "refserve" "init failed"
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "refserve" "apply failed"
proof "a VPC, two subnets and a security group are up, each with a server-assigned id, and the cache is warm."

step "2. a default plan reads them all - the read is drift detection"
cmd "choudoufu plan"
countplan default
echo "default plan: $(calls default) calls, $(hits default) cache hits" | evidence
[ "$(hits default)" = "0" ] || fail "refserve" "a default plan served from cache - it must refresh for drift ($(hits default) hits)"
proof "$(calls default) calls, zero served. A default plan always reads, by ruling."

step "3. -refresh=false serves every instance from cache"
explain \
  "Now the opt-in path. Every one of the four instances is unchanged and" \
  "the cache holds it, so every one must be served: four cache hits, and" \
  "the plan reads none of them. This is the beat that is red today -" \
  "needs-discovery types are not yet vouched on this path (#692), so the" \
  "claim is the contract the fix must meet."
cmd "choudoufu plan -refresh=false"
countplan norefresh -refresh=false
echo "-refresh=false: $(calls norefresh) calls, $(hits norefresh) cache hits (want 4)" | evidence
[ "$(hits norefresh)" -ge 4 ] 2>/dev/null \
  || fail "refserve" "-refresh=false served $(hits norefresh) of 4 instances - needs-discovery instances are not served from cache (#692 increment 3)"
proof "$(hits norefresh) of 4 served from cache; the estate planned without re-reading a single resource."

step "4. serving is existence-vouched - a deleted resource is still caught"
explain \
  "The serving is not blind trust. Each instance is served only because" \
  "the estate sweep confirmed, this run, that its live object still" \
  "carries the estate's marker. Delete one out of band and the sweep no" \
  "longer finds it, so it is not vouched and not served. The plan" \
  "surfaces it even on -refresh=false, because losing an object costs a" \
  "read; it never costs a wrong plan."
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - if a deleted resource is served from cache, the vouch is broken"
  cmd "aws ec2 delete-subnet aws_subnet.a ; choudoufu plan -refresh=false"
  SUB="$(awsl ec2 describe-subnets --filters "Name=tag:tofu-address,Values=aws_subnet.a" --query 'Subnets[0].SubnetId' --output text)"
  [ -n "$SUB" ] && [ "$SUB" != "None" ] || fail "refserve" "could not find subnet a to delete"
  awsl ec2 delete-subnet --subnet-id "$SUB" >/dev/null 2>&1 || fail "refserve" "could not delete subnet a"
  BP="$(cd "$SMOKE_WORK" && chdf plan -refresh=false -input=false -no-color 2>&1 || true)"
  if grep -q "No changes." <<< "$BP"; then
    fail "refserve" "BREAK proved unsafe: a subnet deleted out of band was served from cache and the plan reported No changes"
  fi
  grep -E "aws_subnet.a|will be created|NEEDS_DISCOVERY" <<< "$BP" | head -1 | evidence
  proof "caught - the deleted subnet was NOT served from cache; the sweep did not vouch a gone object, so -refresh=false surfaced it."
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi
proof "existence is confirmed by the sweep before anything is served - proven by the BREAK control."

step "5. teardown"
( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "refserve" "teardown failed"
proof "the estate is gone."

echo "  What you watched: a converged estate of server-assigned resources"
echo "  served entirely from its cache on -refresh=false, reading none of"
echo "  them, while a default plan still refreshes and out-of-band drift is"
echo "  still caught. This is what plans one terralith estate at file speed."
