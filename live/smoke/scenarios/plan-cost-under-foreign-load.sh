# plan-cost-under-foreign-load
# CLAIM 20 - Scale: a plan costs its estate when the account around it is a terralith, and the one account-wide list is named rather than hidden. Needs Go. ~5 min.

# Issue #1032 unit 4. This is claim 14 run against a generated terralith
# instead of against eight hand-written log groups: the estate under test is
# held still and the ACCOUNT is grown, by a whole second terralith carrying a
# different tofu-estate marker. The knobs are the two scales, so the same
# scenario a reader runs in five minutes is the one that produced the
# 3,705-resource row on the claims page:
#
#   FOREIGN_SCALE=1   (default)  79 foreign resources beside the estate
#   FOREIGN_SCALE=50             3,705 of them; a long run, not a smoke run
#   OWNED_SCALE=1     (default)  the estate itself, 74N + 5 resources
#
# The foreign estate is MARKED, not unmarked. plan-cost-tracks-the-estate.sh
# makes its eight foreign resources foreign by applying them under a second
# `live { estate = ... }`, and this scenario matches it: an absent marker is
# excluded by the tag sweep's server-side filter for free, while a marker
# naming another estate has to be fetched and rejected, which is the harder
# case and the one an operator actually has.

FOREIGN_SCALE="${FOREIGN_SCALE:-1}"
OWNED_SCALE="${OWNED_SCALE:-1}"

command -v go >/dev/null 2>&1 || fail "foreignload" "this scenario generates its terraliths with tools/terralith-gen and needs Go on PATH"

WORK="$SMOKE_WORKROOT/foreignload"
mkdir -p "$WORK"
# The compose file interpolates the oracle service, so SMOKE_WORK must be set
# even though this scenario never uses the oracle leg.
SMOKE_WORK="$WORK"; export SMOKE_WORK

# terralith_at generates one terralith into $WORK/$1 at scale $2 with prefix
# $3, then makes it plannable here: floci's provider block in place of the
# generator's (the generator sets skip_requesting_account_id, which is right
# for output that must stand alone and wrong for anything resolving an ECS
# identity, because an ECS ARN carries the account id - issue #572), and a
# live block naming estate $4.
terralith_at() {
  local dir="$1" scale="$2" prefix="$3" estate="$4"
  ( cd "$ROOT" && go run ./tools/terralith-gen -scale "$scale" -prefix "$prefix" -out "$WORK/$dir" >"$WORK/$dir.gen" 2>&1 ) \
    || { cat "$WORK/$dir.gen" >&2; fail "foreignload" "terralith-gen -scale $scale -prefix $prefix failed"; }
  python3 - "$WORK/$dir/versions.tf" "$estate" <<'PY'
import sys
path, estate = sys.argv[1], sys.argv[2]
src = open(path).read()
head = src[:src.index('provider "aws" {')]
provider = '''provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}
'''
anchor = 'required_version = ">= 1.5.0"'
if anchor not in head:
    raise SystemExit("terralith-gen's versions.tf template changed: no %r" % anchor)
live = anchor + '\n\n  live {\n    estate = "' + estate + '"\n\n    record_store "local" {\n      path = ".tofu-records"\n    }\n  }'
open(path, "w").write(head.replace(anchor, live, 1) + provider)
PY
  [ $? -eq 0 ] || fail "foreignload" "could not rewrite $WORK/$dir/versions.tf"
  sed -n '1s/.*: //p' "$WORK/$dir.gen"
}

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
LOGS="$SMOKE_WORKROOT/foreignload-logs"; mkdir -p "$LOGS"

# planplan runs one plan with the debug log captured and prints the number of
# requests it put on the wire. Same counter plan-cost-tracks-the-estate.sh
# uses, for the same reason: cost is measured in API calls, the work that
# actually scales, and "HTTP Request Sent" is the one line both the AWS
# provider's own logger and choudoufu's Cloud Control and Tagging clients
# emit per request (internal/live/cloudcontrol/client.go).
planplan() { # $1=dir $2=logname $3+=extra flags
  local dir="$1" name="$2"; shift 2
  ( cd "$WORK/$dir" && TF_LOG=debug TF_LOG_PATH="$LOGS/$name.log" "$TOFU" plan -input=false -no-color "$@" >"$LOGS/$name.out" 2>&1 || true )
  countin "$name" "HTTP Request Sent"
}

# countin prints how many times a pattern occurs in one captured plan's log.
# Two things went wrong here before it looked like this, and both produced a
# number that read fine:
#
#   grep -c prints 0 when nothing matches AND exits 1, so the idiomatic
#   `|| echo 0` prints TWO zeros; every arithmetic test then sees "0\n0".
#
#   grep -c counts LINES, and TF_LOG's writers are concurrent with no
#   synchronisation, so under a wide plan two records land on one line and
#   the count silently drops. That is how a draft of this scenario reported
#   more Cloud Control lists than the plan made requests. -o counts
#   occurrences, which is what a request count has to be.
countin() {
  local n
  n="$(grep -o "$2" "$LOGS/$1.log" 2>/dev/null | wc -l | tr -d " ")"
  echo "${n:-0}"
}

# cclists counts the account-wide Cloud Control lists in one captured plan.
# Cloud Control has no server-side tag filter at all, so this is the call in
# a plan that reads the account rather than the estate, and it is counted on
# its own rather than folded into a total.
#
# The pattern is anchored on "HTTP Request Sent" and not on rpc.method alone
# for a measured reason: internal/live/cloudcontrol/client.go logs a Request
# line AND a Response line per call, both carrying the same rpc.service and
# rpc.method, so the unanchored pattern counts every call twice. It reported
# 870 Cloud Control lists inside a 736-request plan before this said so.
cclists() { countin "$1" "stateless/cloudcontrolapi: HTTP Request Sent: rpc.service=Cloud Control rpc.method=ListResources"; }

planned_nothing() { grep -q "No changes." "$LOGS/$1.out" 2>/dev/null; }

# unfiltered_types prints, from the run's OWN debug log, every type it
# listed without a server-side estate filter and the reason it gives. This
# is the mechanism rather than a symptom: internal/live/discovery's scanType
# logs the line when it cannot put a tag filter on a list, and
# scanTypeCloudControl logs its own because that API has no tag filter at
# all - and its line carries how many objects the account-wide list brought
# back, which is the number that grows with the account even when the call
# count does not.
unfiltered_types() {
  grep -oE "stateless/discovery: listing [a-z0-9_]+ (unfiltered \\(|via Cloud Control \\()[^)]*\\)(, [0-9]+ resources)?" "$LOGS/$1.log" 2>/dev/null \
    | sed "s/stateless.discovery: listing //" | sort | uniq -c | sed "s/^ *//" || true
}

step "the claim"
explain \
  "Claim 14 shows that a plan costs its estate rather than its account on" \
  "an account holding twelve resources. This is the same question asked" \
  "where it is load-bearing: the account is a terralith. A whole second" \
  "estate - generated by tools/terralith-gen, applied under its own" \
  "tofu-estate marker - lands beside the one under test, and the estate is" \
  "replanned unchanged. A bound state file pays at least one read for" \
  "every resource in it, so if this plan's cost tracked the ACCOUNT it" \
  "would climb by roughly a call per foreign resource. It does not. What" \
  "it does climb by is measured here rather than rounded to zero, and the" \
  "legs that read the account rather than the estate are named."

step "1. stand up the estate, and plan it alone"
cmd "go run ./tools/terralith-gen -scale $OWNED_SCALE -prefix ow ; choudoufu apply ; choudoufu plan"
terralith_at owned "$OWNED_SCALE" ow ow-estate | evidence
( cd "$WORK/owned" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "foreignload" "owned init failed"
( cd "$WORK/owned" && chdf apply -auto-approve -input=false -no-color >"$LOGS/owned-apply.out" 2>&1 ) \
  || { tail -30 "$LOGS/owned-apply.out" >&2; fail "foreignload" "owned apply failed"; }
grep "Apply complete" "$LOGS/owned-apply.out" | evidence
OWNED_ALONE="$(planplan owned owned-alone)"
planned_nothing owned-alone || fail "foreignload" "the estate's own plan was not empty, so its call count is not comparable with anything below"
[ "$OWNED_ALONE" -gt 0 ] 2>/dev/null || fail "foreignload" "the estate plan made no measurable calls: $OWNED_ALONE"
OWNED_ALONE_CC="$(cclists owned-alone)"
echo "plan of ow-estate, account holds only ow-estate: $OWNED_ALONE calls ($OWNED_ALONE_CC of them account-wide Cloud Control lists)" | evidence
proof "$OWNED_ALONE calls to plan the estate on an account that holds nothing else."

step "2. grow the ACCOUNT into a terralith, and replan the estate"
explain \
  "A second terralith joins the account under its own tofu-estate marker." \
  "Nothing about the estate under test changes: same configuration, same" \
  "resources, same record store. The only thing that changed is how much" \
  "of somebody else's infrastructure the account now holds."
cmd "go run ./tools/terralith-gen -scale $FOREIGN_SCALE -prefix fg ; choudoufu apply (estate fg-estate) ; choudoufu plan (ow-estate again)"
terralith_at foreign "$FOREIGN_SCALE" fg fg-estate | evidence
( cd "$WORK/foreign" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "foreignload" "foreign init failed"
( cd "$WORK/foreign" && chdf apply -auto-approve -input=false -no-color >"$LOGS/foreign-apply.out" 2>&1 ) \
  || { tail -30 "$LOGS/foreign-apply.out" >&2; fail "foreignload" "foreign apply failed"; }
grep "Apply complete" "$LOGS/foreign-apply.out" | evidence
FOREIGN_N="$(sed -n 's/.*Resources: \([0-9]*\) added.*/\1/p' "$LOGS/foreign-apply.out" | head -1)"
[ -n "$FOREIGN_N" ] || fail "foreignload" "could not read how many foreign resources were applied"

OWNED_LOADED="$(planplan owned owned-loaded)"
planned_nothing owned-loaded || fail "foreignload" "the estate's replan was not empty once the foreign estate existed"
OWNED_LOADED_CC="$(cclists owned-loaded)"
GROWTH=$(( OWNED_LOADED - OWNED_ALONE ))
echo "plan of ow-estate, account holds only ow-estate:                 $OWNED_ALONE calls" | evidence
echo "plan of ow-estate, account also holds $FOREIGN_N foreign resources: $OWNED_LOADED calls" | evidence
echo "growth: $GROWTH calls for $FOREIGN_N foreign resources" | evidence

# The bound, and why it is this one rather than a round number somebody
# liked. A bound state file is read end to end: every resource in it costs
# at least one call on every plan, so "cost tracks the account" has a
# concrete floor of one call per foreign resource. Half of that floor is
# the threshold. It is not "the number did not move" - it did move, by a
# little, and step 3 says exactly where - it is "the number did not move
# the way a state file would make it move".
[ "$GROWTH" -ge 0 ] 2>/dev/null || fail "foreignload" "the replan cost LESS than the plan before the foreign estate existed ($OWNED_ALONE -> $OWNED_LOADED); something other than the foreign estate changed between them"
if [ $(( GROWTH * 2 )) -ge "$FOREIGN_N" ]; then
  fail "foreignload" "the estate's plan grew $GROWTH calls for $FOREIGN_N foreign resources, at least half a call each - that is the shape a bound state file has, and the estate scoping is not holding"
fi
proof "$GROWTH calls of growth for $FOREIGN_N foreign resources. A state file would have paid at least $FOREIGN_N."

step "3. what grew, and what did not"
explain \
  "The growth above is not zero and this scenario will not print it as if" \
  "it were. It comes from the legs that read the ACCOUNT rather than the" \
  "estate, and the run names them itself: a type whose provider list" \
  "resource offers no filter block cannot be filtered server-side, and" \
  "Cloud Control offers no tag filter on any type at all. Both are" \
  "filtered on this side of the wire, so the CALLS are few and flat while" \
  "the BYTES and any per-object refinement they force are not - which is" \
  "the term issue #622 named. Everything below is printed from the run's" \
  "own debug log; anything not named there was estate-scoped server-side" \
  "and cost this plan the same whatever the neighbour brought."
cmd "grep 'listing .* unfiltered\\|via Cloud Control' \$TF_LOG_PATH"
echo "-- the estate alone --" | evidence
unfiltered_types owned-alone | evidence
echo "-- the same estate, beside $FOREIGN_N foreign resources --" | evidence
unfiltered_types owned-loaded | evidence
echo "account-wide Cloud Control list CALLS - alone: $OWNED_ALONE_CC   ·   under load: $OWNED_LOADED_CC" | evidence
[ "$OWNED_LOADED_CC" = "$OWNED_ALONE_CC" ] \
  || fail "foreignload" "the account-wide Cloud Control list count moved with the foreign population ($OWNED_ALONE_CC -> $OWNED_LOADED_CC); that is a finding to record against issue #1032, not a threshold to edit"
proof "the account-wide Cloud Control list ran $OWNED_LOADED_CC times either way, and every list this plan could not scope to the estate is named above with the run's own reason for it."

step "4. what reading the whole terralith would cost"
explain \
  "For scale, ask the account-wide question instead of the estate one -" \
  "what -adoption-only answers, and the shape a bound state file forces on" \
  "every plan whether you meant it or not. This is the same account, the" \
  "same directory and the same binary; only the scoping is different."
cmd "choudoufu plan -adoption-only   # account-wide"
ACCOUNT_WIDE="$(planplan owned account-wide -adoption-only)"
ACCOUNT_WIDE_CC="$(cclists account-wide)"
echo "scoped to ow-estate: $OWNED_LOADED calls   ·   account-wide scan: $ACCOUNT_WIDE calls ($ACCOUNT_WIDE_CC Cloud Control lists)" | evidence
[ "$ACCOUNT_WIDE" -gt "$OWNED_LOADED" ] 2>/dev/null \
  || fail "foreignload" "the account-wide scan was not more expensive than the scoped plan ($ACCOUNT_WIDE vs $OWNED_LOADED) - the scoping saved nothing"
RATIO=$(( ACCOUNT_WIDE / (OWNED_LOADED>0?OWNED_LOADED:1) ))
proof "scoping turned a ${ACCOUNT_WIDE}-call account scan into ${OWNED_LOADED} calls, about ${RATIO}x less, on an account holding $FOREIGN_N foreign resources."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - defeat the estate filter; the cost must explode"
  explain \
    "Everything above rests on one branch: internal/live/discovery's" \
    "scanType puts a server-side estate filter on every list it can, and" \
    "drops it the moment a run asks for unclaimed resources, because a" \
    "server-side estate filter would hide them. Defeat the filter by" \
    "asking that question of the same estate. If the cost does NOT jump," \
    "then the flat column above was flat for some other reason and this" \
    "scenario proves nothing about the filter."
  cmd "choudoufu plan -adoption-only   # estate filter defeated"
  BROKEN="$(planplan owned break-wide -adoption-only)"
  echo "estate-filtered: $OWNED_LOADED calls   ·   filter defeated: $BROKEN calls" | evidence
  if [ "$BROKEN" -le $(( OWNED_LOADED * 3 )) ] 2>/dev/null; then
    fail "foreignload" "defeating the estate filter did NOT explode the cost ($OWNED_LOADED -> $BROKEN) - the filter was not what made the plan cheap"
  fi
  proof "caught - without the estate filter the same plan costs $BROKEN calls, not $OWNED_LOADED, on an account carrying $FOREIGN_N foreign resources. The filter is the whole saving."
  exit 0
fi

step "5. teardown"
cmd "choudoufu apply -destroy (both estates)"
( cd "$WORK/owned" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "foreignload" "owned teardown failed"
( cd "$WORK/foreign" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "foreignload" "foreign teardown failed"
proof "both terraliths gone."

echo "  What you watched: an estate that cost $OWNED_ALONE calls to plan alone"
echo "  cost $OWNED_LOADED beside $FOREIGN_N foreign resources under another marker -"
echo "  $GROWTH calls of growth where a bound state file would have paid at least"
echo "  $FOREIGN_N - while asking the same account the account-wide question cost"
echo "  $ACCOUNT_WIDE. The legs that read the account rather than the estate were"
echo "  named from the run's own log and counted on their own, because a claim"
echo "  that hides its own exception is not a claim."
