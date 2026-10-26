# two-creates-one-address
# CLAIM 2 (aws) - Nothing is held. ~3 min.
#
# This proof: Two creates of the same server-assigned resource both land,
# and the next plan names both live ids and refuses (live/CONCURRENCY.md,
# "Two creates of the same server-assigned resource"; GitHub issue #2254).
#
# A draft until its first green run on both arms (drafts/README.md). The
# client-named race is no-self-managed-locks step 2: the cloud refuses the
# second create of one name. A VPC has no name the cloud enforces, so both
# creates succeed, and collisionProblem (internal/live/discovery) is what
# reports the pair.

SMOKE_WORK="$SMOKE_WORKROOT/vpcrace"
ESTATE="smoke-vpcrace"
mkdir -p "$SMOKE_WORK/a" "$SMOKE_WORK/b"; export SMOKE_WORK
for c in a b; do
  sed "s/live-e2e-block/$ESTATE/" "$ROOT/live/e2e/estate-block/versions.tf" > "$SMOKE_WORK/$c/versions.tf"
  cat > "$SMOKE_WORK/$c/vpc.tf" <<TFEOF
resource "aws_vpc" "main" {
  cidr_block = "10.9.0.0/16"
  tags = {
    tofu-estate  = "$ESTATE"
    tofu-address = "aws_vpc.main"
  }
}
TFEOF
done

step "the claim"
explain \
  "Two applies of one estate, started together, each create aws_vpc.main." \
  "A VPC's id is assigned by the server, so the cloud has no name to" \
  "refuse the second create on and both land: two live VPCs wear the" \
  "same estate and address markers. Nothing here takes a lock to stop" \
  "that. What the claim promises is that the next plan reports the pair" \
  "by id and refuses, rather than binding one and leaving the other to" \
  "be found later."

# The binary under test. BREAK=1 swaps in one whose discovery binds the
# first claimant of a scalar address instead of reporting the collision.
if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a discovery that binds one claimant must be caught"
  explain \
    "The break is in the binary: discovery's scalar collision arm binds" \
    "the first of the two claimants and reports nothing. Built with go" \
    "build -overlay, so the source tree is never touched; it needs this" \
    "checkout and Go."
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "vpcrace" "BREAK=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "vpcrace" "BREAK=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/live/discovery/discovery.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/discovery.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
old = "\n\t\t\t\tdiags = diags.Append(problemDiag(res, collisionProblem(req, typeName, entry)))\n"
assert src.count(old) == 1, "the break patch no longer matches discovery's scalar collision arm"
new = ("\n\t\t\t\t// BREAK: bind the first claimant, report nothing\n"
       "\t\t\t\tif diag, hasProblem := bindClaimant(res, bound, typeName, escaped, entry.res.Addr, entry.claimants[0]); hasProblem {\n"
       "\t\t\t\t\tdiags = diags.Append(diag)\n"
       "\t\t\t\t}\n")
open(sys.argv[2], "w").write(src.replace(old, new))
PYEOF
  [ -s "$SMOKE_WORK/break/discovery.go" ] || fail "vpcrace" "the break patch did not apply to $SRC, so this arm would pass by testing the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/discovery.go" > "$SMOKE_WORK/break/overlay.json"
  cmd "go build -overlay overlay.json ./cmd/choudoufu   # a collision binds its first claimant"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "vpcrace" "the broken binary did not build"
  TOFU="$SMOKE_WORK/break/choudoufu"
fi
flat() { tr '\n' ' ' | sed 's/│/ /g' | tr -s ' '; }
marked_vpcs() {
  awsl ec2 describe-vpcs \
    --filters "Name=tag:tofu-estate,Values=$ESTATE" "Name=tag:tofu-address,Values=aws_vpc.main" \
    --query 'Vpcs[].VpcId' --output text | tr '\t' '\n' | grep -v '^$' | sort || true
}

step "1. one estate, two checkouts of it, a proxy that makes the race a race"
stack_up
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
[ -z "$(marked_vpcs)" ] || fail "vpcrace" "a VPC already carries $ESTATE's aws_vpc.main marker before the race: $(marked_vpcs)"
logged two-creates-one-address-a-init "vpcrace" "init failed in a" -- in_dir "$SMOKE_WORK/a" chdf init -input=false -no-color
logged two-creates-one-address-b-init "vpcrace" "init failed in b" -- in_dir "$SMOKE_WORK/b" chdf init -input=false -no-color
explain \
  "Two applies started together can still run in sequence: one creates" \
  "before the other has planned, the second plan binds the first VPC," \
  "and nothing raced. The proxy holds each CreateVpc until both have" \
  "arrived. A create is sent only after its own plan, so two held" \
  "creates are two plans made against an estate with no VPC in it."
python3 "$SMOKE_DIR/s3proxy.py" "$FLOCI_PORT" "$SMOKE_WORK" 2>"$SMOKE_WORKROOT/logs/vpcrace-proxy.err" &
PROXY_PID=$!
trap 'kill $PROXY_PID 2>/dev/null || true; cleanup' EXIT
for _ in $(seq 1 50); do [ -s "$SMOKE_WORK/proxy.port" ] && break; sleep 0.1; done
[ -s "$SMOKE_WORK/proxy.port" ] || fail "vpcrace" "the proxy never started"
PROXY_URL="http://localhost:$(cat "$SMOKE_WORK/proxy.port")"
rm -f "$SMOKE_WORK/gated"
echo "Action=CreateVpc 2" > "$SMOKE_WORK/gate"
proof "every AWS call the two applies make goes through it, and a CreateVpc waits for its twin."

step "2. the race - two applies, one address, at the same moment"
cmd "(apply in a &) ; (apply in b &)   # both through the proxy"
# `&& rc=0 || rc=$?`, never `; echo $?`: smoke.sh runs under set -e, and an
# apply that exits nonzero would end its subshell before its code was
# written (no-self-managed-locks, #1590/#1646).
( cd "$SMOKE_WORK/a" && { AWS_ENDPOINT_URL="$PROXY_URL" chdf apply -auto-approve -input=false -no-color > "$SMOKE_WORK/a.out" 2>&1 && rc=0 || rc=$?; echo "$rc" > "$SMOKE_WORK/a.rc"; } ) &
PID_A=$!
( cd "$SMOKE_WORK/b" && { AWS_ENDPOINT_URL="$PROXY_URL" chdf apply -auto-approve -input=false -no-color > "$SMOKE_WORK/b.out" 2>&1 && rc=0 || rc=$?; echo "$rc" > "$SMOKE_WORK/b.rc"; } ) &
PID_B=$!
# Bounded, and on the two PIDs, never a bare `wait` (#1593): the proxy lets a
# lone CreateVpc go after 60s, so both applies end well inside this.
for _ in $(seq 1 2400); do
  [ -s "$SMOKE_WORK/a.rc" ] && [ -s "$SMOKE_WORK/b.rc" ] && break
  sleep 0.05
done
if ! { [ -s "$SMOKE_WORK/a.rc" ] && [ -s "$SMOKE_WORK/b.rc" ]; }; then
  kill "$PID_A" "$PID_B" 2>/dev/null || true
  fail "vpcrace" "an apply was still running 120s after both started: a=$(tail -3 "$SMOKE_WORK/a.out" 2>/dev/null) b=$(tail -3 "$SMOKE_WORK/b.out" 2>/dev/null)"
fi
wait "$PID_A" "$PID_B" 2>/dev/null || true
rm -f "$SMOKE_WORK/gate"
A_RC="$(cat "$SMOKE_WORK/a.rc")"; B_RC="$(cat "$SMOKE_WORK/b.rc")"
GATED="$(grep -c '' "$SMOKE_WORK/gated" 2>/dev/null || echo 0)"
[ "$GATED" = "2" ] \
  || fail "vpcrace" "$GATED CreateVpc call(s) reached the proxy, not 2: the second apply planned after the first had created, so the two runs were a sequence and not a race. a (exit $A_RC): $(tail -3 "$SMOKE_WORK/a.out") b (exit $B_RC): $(tail -3 "$SMOKE_WORK/b.out")"
grep -q "Acquiring state lock" "$SMOKE_WORK/a.out" "$SMOKE_WORK/b.out" \
  && fail "vpcrace" "an apply printed 'Acquiring state lock' - a lock was taken after all"
echo "both CreateVpc calls held until both arrived, then forwarded; apply a exited $A_RC, apply b exited $B_RC" | evidence
proof "two plans, each made against an estate with no VPC in it, each sent its create."

step "3. the cloud holds two VPCs wearing one address"
explain \
  "Read from the cloud, not from either run's output: every VPC that" \
  "carries this estate's tofu-estate and tofu-address markers."
cmd "aws ec2 describe-vpcs --filters Name=tag:tofu-estate,Values=$ESTATE Name=tag:tofu-address,Values=aws_vpc.main"
LIVE="$(marked_vpcs)"
N_LIVE="$(grep -c '' <<< "$LIVE" || true)"
[ -n "$LIVE" ] && [ "$N_LIVE" = "2" ] \
  || fail "vpcrace" "the cloud holds $N_LIVE VPC(s) carrying $ESTATE's aws_vpc.main marker, not 2: $LIVE"
VPC_1="$(sed -n 1p <<< "$LIVE")"; VPC_2="$(sed -n 2p <<< "$LIVE")"
echo "$VPC_1 and $VPC_2 both carry tofu-estate=$ESTATE, tofu-address=aws_vpc.main" | evidence
proof "both creates landed. The cloud had no name to refuse the second on."

step "4. the next plan names both and refuses"
explain \
  "Neither VPC is more the estate's than the other, and nothing in the" \
  "estate says which one to keep. The plan must say so, by id, and stop."
cmd "choudoufu plan   # in checkout a"
P_RC=0
P_OUT="$(cd "$SMOKE_WORK/a" && chdf plan -input=false -no-color 2>&1)" || P_RC=$?
P_FLAT="$(flat <<< "$P_OUT")"
MISSING=""
grep -q "$VPC_1" <<< "$P_FLAT" || MISSING="$MISSING $VPC_1"
grep -q "$VPC_2" <<< "$P_FLAT" || MISSING="$MISSING $VPC_2"
REFUSED=0
grep -q "A human has to resolve the collision before this estate can be planned" <<< "$P_FLAT" && REFUSED=1
if [ "${BREAK:-0}" = "1" ]; then
  if [ "$P_RC" = "0" ] || [ "$REFUSED" = "0" ] || [ -n "$MISSING" ]; then
    echo "plan exit $P_RC; refusal printed: $REFUSED; ids missing from the output:${MISSING:- none}" | evidence
    proof "caught - with discovery binding one claimant, the plan no longer names the pair and refuses. Two VPCs wear one address and this run said nothing about it."
    awsl ec2 delete-vpc --vpc-id "$VPC_1" >/dev/null 2>&1 || true
    awsl ec2 delete-vpc --vpc-id "$VPC_2" >/dev/null 2>&1 || true
    exit 0
  fi
  fail "vpcrace" "the binary built to bind one claimant still refused the collision naming both ids, so the break did not take and this control proves nothing: $P_OUT"
fi
[ "$P_RC" != "0" ] || fail "vpcrace" "the plan exited 0 with two live VPCs claiming aws_vpc.main: $P_OUT"
[ -z "$MISSING" ] || fail "vpcrace" "the plan did not name every claimant (missing:$MISSING): $P_OUT"
[ "$REFUSED" = "1" ] || fail "vpcrace" "the plan failed without the collision refusal: $P_OUT"
grep -oE "[0-9]+ live aws_vpc resources carry estate .{0,200}" <<< "$P_FLAT" | head -1 | evidence
proof "the plan failed, named $VPC_1 and $VPC_2, and asked a human to resolve the collision."

step "5. the human resolves it, the estate is clean again"
cmd "aws ec2 delete-vpc --vpc-id $VPC_2 ; choudoufu plan"
awsl ec2 delete-vpc --vpc-id "$VPC_2" || fail "vpcrace" "could not delete $VPC_2"
CLEAN="$(cd "$SMOKE_WORK/a" && chdf plan -input=false -no-color 2>&1)" \
  || fail "vpcrace" "the plan after deleting $VPC_2 failed: $CLEAN"
grep -q "No changes." <<< "$CLEAN" || fail "vpcrace" "the estate is not clean after resolving the collision: $CLEAN"
grep -E 'No changes\.' <<< "$CLEAN" | head -1 | evidence
proof "one delete was the whole resolution; the estate bound the VPC that was left."

step "6. teardown"
cmd "choudoufu apply -destroy -auto-approve"
D_OUT="$(cd "$SMOKE_WORK/a" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "vpcrace" "teardown failed: $D_OUT"
destroyed_exactly vpcrace 1 "$D_OUT"
[ -z "$(marked_vpcs)" ] || fail "vpcrace" "a marked VPC is still live after the destroy: $(marked_vpcs)"
proof "gone."

echo "  What you watched: two applies of one estate each created aws_vpc.main,"
echo "  the cloud kept both, the next plan named both ids and refused, and one"
echo "  delete by a person put the estate back to clean."
