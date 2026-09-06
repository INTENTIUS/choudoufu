# the-boundary-holds-across-regions
# CLAIM 16 - The boundary holds across provider configurations: one estate spans regions, and every answer a plan gives is about one provider configuration. ~3 min.

SMOKE_WORK="$SMOKE_WORKROOT/tworegions"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
LOGDIR="$SMOKE_WORKROOT/tworegions-logs"; mkdir -p "$LOGDIR"

LG_NAME="/smoke-two-regions/app"
EAST_ARN="arn:aws:logs:us-east-1:000000000000:log-group:$LG_NAME"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"

# The estate, written here rather than copied from live/e2e so the two
# provider configurations and the mirrored name are readable in one place.
#
#   aws.east / aws.west   two aliased provider configurations, one region
#                         each, against floci's single endpoint. Neither is
#                         the default configuration: every block names the
#                         one it belongs to.
#   the log group pair    ONE client-chosen name declared in both regions.
#                         A log group's import identity IS its name and
#                         DescribeLogGroups is region-scoped, so these are
#                         two distinct live objects answering to one import
#                         identity. That is issue #745's shape.
#   the vpc pair          server-assigned: nothing in the configuration says
#                         which vpc- id it is, so each is found by listing
#                         for its marker in its own region. This is the pair
#                         the state cache serves on -refresh=false.
#   the bucket            declared only under east, and S3's list is
#                         account-global rather than region-scoped. It is
#                         the type the west pass must not list.
write_estate() {
  local west_region="${1:-us-west-2}"
  cat > "$SMOKE_WORK/main.tf" <<TFEOF
terraform {
  required_version = ">= 1.5.0"

  live {
    estate = "smoke-two-regions"
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  alias                       = "east"
  region                      = "us-east-1"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

provider "aws" {
  alias                       = "west"
  region                      = "${west_region}"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

resource "aws_cloudwatch_log_group" "east" {
  provider          = aws.east
  name              = "${LG_NAME}"
  retention_in_days = 1
}

resource "aws_cloudwatch_log_group" "west" {
  provider          = aws.west
  name              = "${LG_NAME}"
  retention_in_days = 1
}

resource "aws_vpc" "east" {
  provider   = aws.east
  cidr_block = "10.70.0.0/16"
}

resource "aws_vpc" "west" {
  provider   = aws.west
  cidr_block = "10.71.0.0/16"
}

resource "aws_s3_bucket" "global" {
  provider = aws.east
  bucket   = "tofu-smoke-two-regions-global"
}
TFEOF
}

# drop_block removes one resource block from the estate by its header line.
# The blocks above are all closed by a bare "}" in column one, which is what
# lets a header-to-first-bare-brace cut be exact rather than a guess.
drop_block() {
  python3 - "$SMOKE_WORK/main.tf" "$1" <<'PYEOF'
import sys
path, header = sys.argv[1], sys.argv[2]
s = open(path).read()
i = s.index(header)
j = s.index('\n}\n', i) + 3
open(path, 'w').write(s[:i] + s[j:])
PYEOF
}

plan_rf() {
  (cd "$SMOKE_WORK" && TF_LOG=debug TF_LOG_PATH="$LOGDIR/$1.log" "$TOFU" plan -refresh=false -input=false -no-color 2>&1 | grep -v '^discovering:')
}
plan_default() {
  (cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 | grep -v '^discovering:')
}
# requests_in counts the signed requests in one debug stream that were
# signed FOR a region. SigV4's credential scope carries the region the
# request was signed for, so this attributes work to the provider
# configuration that did it without trusting any counter of ours.
requests_in() { grep -oE "Credential=test/[0-9]{8}/$2/" "$LOGDIR/$1.log" 2>/dev/null | wc -l | tr -d ' '; }
# lists_of counts the estate sweep's own listing passes for one type.
lists_of() { grep -c "listing $2 " "$LOGDIR/$1.log" 2>/dev/null || true; }

step "the claim"
explain \
  "The boundary holds across provider configurations. One estate is" \
  "allowed to span regions and accounts - the same tofu-estate marker in" \
  "both, one record store - and every answer a plan gives is about ONE" \
  "provider configuration. Two regions that mirror a client-chosen name" \
  "hold two distinct live objects answering to one import identity, and" \
  "neither may answer for the other: delete one region's object and that" \
  "region's instance is named, while the other region keeps being served" \
  "from cache. What a pass lists is its own configuration's declarations," \
  "so a type only one region declares is listed once, not once per region." \
  "And the honest edge: the sweep looks where a provider configuration" \
  "points, so removing a region's last declaration takes the region out" \
  "of the sweep with it."

write_estate
stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "1. two regions, one estate"
explain \
  "One apply over both provider configurations. Then the AWS CLI reads" \
  "each region directly, with no choudoufu in the loop: two log groups" \
  "with the SAME name and DIFFERENT region-qualified ARNs, each carrying" \
  "the same tofu-estate and its own tofu-address. That is the answer to" \
  "'is this one estate or two' - it is one, and the marker says so in" \
  "both regions. Then a -refresh=false plan, with the work attributed by" \
  "the region each request was signed for."
cmd "choudoufu apply -auto-approve ; aws logs describe-log-groups (each region)"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "regions" "init failed"
A1="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "regions" "apply failed: $A1"
ADDED="$(grep -oE 'Resources: [0-9]+ added' <<< "$A1" | grep -oE '[0-9]+')"
[ "$ADDED" = "5" ] || fail "regions" "the apply built $ADDED resources, not the fixture's 5: $A1"

E_ARN="$(awsl --region us-east-1 logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
W_ARN="$(awsl --region us-west-2 logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
[ -n "$E_ARN" ] || fail "regions" "aws.east (us-east-1) holds no log group named $LG_NAME"
[ -n "$W_ARN" ] || fail "regions" "aws.west (us-west-2) holds no log group named $LG_NAME"
[ "$E_ARN" != "$W_ARN" ] || fail "regions" "both provider configurations returned the same ARN ($E_ARN) - this is one object, not a mirrored pair, and nothing below would mean anything"
case "$E_ARN" in *:us-east-1:*) ;; *) fail "regions" "aws.east's log group is not in us-east-1: $E_ARN" ;; esac
case "$W_ARN" in *:us-west-2:*) ;; *) fail "regions" "aws.west's log group is not in us-west-2: $W_ARN" ;; esac
E_TAGS="$(awsl --region us-east-1 logs list-tags-for-resource --resource-arn "$E_ARN" --output json)"
W_TAGS="$(awsl --region us-west-2 logs list-tags-for-resource --resource-arn "$W_ARN" --output json)"
grep -q '"tofu-estate": "smoke-two-regions"' <<< "$E_TAGS" || fail "regions" "aws.east's object does not carry the estate marker: $E_TAGS"
grep -q '"tofu-estate": "smoke-two-regions"' <<< "$W_TAGS" || fail "regions" "aws.west's object does not carry the estate marker: $W_TAGS"
grep -q '"tofu-address": "aws_cloudwatch_log_group.east"' <<< "$E_TAGS" || fail "regions" "aws.east's object does not carry its own address marker: $E_TAGS"
grep -q '"tofu-address": "aws_cloudwatch_log_group.west"' <<< "$W_TAGS" || fail "regions" "aws.west's object does not carry its own address marker: $W_TAGS"
RECDIR="$SMOKE_WORK/.tofu-records/tofu-records/smoke-two-regions"
[ -d "$RECDIR" ] || fail "regions" "no record store appeared for the estate at $RECDIR"
RECS="$(find "$RECDIR" -type f ! -name '.store-sentinel' | wc -l | tr -d ' ')"
{ echo "aws.east  (us-east-1) $E_ARN"; echo "aws.west  (us-west-2) $W_ARN"; echo "one record store, $RECS record(s), both regions' instances in it"; } | evidence
proof "one estate, two provider configurations: aws.east and aws.west hold two distinct objects answering to the same name $LG_NAME, both marked tofu-estate=smoke-two-regions, both recorded in the one store beside the module."

explain \
  "Now the plan. -refresh=false is the path that serves converged" \
  "instances from the state cache, so it is also the path where one" \
  "region answering for another would be invisible. Both regions must" \
  "bind, the plan must be empty, and the work must be attributable: SigV4" \
  "signs every request for exactly one region, so the credential scope in" \
  "the debug stream is the request count per provider configuration -" \
  "counted from the wire, not from a counter of ours."
cmd "choudoufu plan -refresh=false (TF_LOG=debug)"
P_BIND="$(plan_rf bind)" || fail "regions" "the two-region -refresh=false plan failed"
grep -q "No changes." <<< "$P_BIND" || fail "regions" "the two-region estate did not plan clean: $P_BIND"
REQ_EAST="$(requests_in bind us-east-1)"
REQ_WEST="$(requests_in bind us-west-2)"
[ "$REQ_EAST" -gt 0 ] || fail "regions" "no request was signed for us-east-1 - the aws.east pass did no work"
[ "$REQ_WEST" -gt 0 ] || fail "regions" "no request was signed for us-west-2 - the aws.west pass never reached the region it names, and every region claim below would be vacuous"
HITS_BIND="$(grep -c 'state cache hit' "$LOGDIR/bind.log" || true)"
echo "per-pass requests: aws.east (us-east-1) $REQ_EAST, aws.west (us-west-2) $REQ_WEST; $HITS_BIND instance(s) served from the cache" | evidence
grep -E 'No changes\.' <<< "$P_BIND" | head -1 | evidence
proof "both provider configurations bound their own half and the estate planned empty, at $REQ_EAST requests signed for aws.east's region and $REQ_WEST for aws.west's."

explain \
  "The listing half of the same question. A pass lists the types its own" \
  "provider configuration declares. The mirrored log group is declared" \
  "under both, so it is listed twice - once per region, which is the" \
  "only way two region-scoped objects can be seen at all. The S3 bucket" \
  "is declared only under east, and S3's list is account-global: if a" \
  "pass listed every type regardless of scope, aws.west would list it" \
  "again and count the same account-global population twice."
cmd "grep 'listing' the debug stream"
LIST_LG="$(lists_of bind aws_cloudwatch_log_group)"
LIST_S3="$(lists_of bind aws_s3_bucket)"
echo "aws_cloudwatch_log_group (declared under aws.east and aws.west): $LIST_LG listing pass(es)" | evidence
echo "aws_s3_bucket (declared under aws.east only, account-global): $LIST_S3 listing pass(es)" | evidence
[ "$LIST_LG" = "2" ] || fail "regions" "the mirrored log group was listed $LIST_LG time(s), not once per provider configuration - a region-scoped type listed anything but per-region cannot see both objects"
[ "$LIST_S3" = "1" ] || fail "regions" "the east-only global bucket was listed $LIST_S3 time(s); aws.west declares none of it and must not list it"
proof "each pass listed its own configuration's declarations: the mirrored type once per region, the east-only global type once across both passes."

step "2. a delete in one region is seen in that region"
explain \
  "This is issue #745's step. aws.west's log group is deleted out of" \
  "band with the AWS CLI. Its twin in us-east-1 is untouched and still" \
  "answers to the same import identity, $LG_NAME - so a run that" \
  "keyed its evidence by identity alone, with no notion of which" \
  "provider configuration saw it, would take east's object as proof that" \
  "west's still exists and report a dead instance as unchanged. The next" \
  "-refresh=false plan must name aws.west's instance, and only" \
  "aws.west's, while aws.east's half keeps being served from the cache."
cmd "aws --region us-west-2 logs delete-log-group ; choudoufu plan -refresh=false"
awsl --region us-west-2 logs delete-log-group --log-group-name "$LG_NAME" \
  || fail "regions" "could not delete aws.west's log group out of band"
GONE="$(awsl --region us-west-2 logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
[ -z "$GONE" ] || fail "regions" "aws.west's log group survived the delete ($GONE) - the drift is not real"
STILL="$(awsl --region us-east-1 logs describe-log-groups --query "logGroups[?logGroupName=='$LG_NAME'].arn" --output text)"
[ "$STILL" = "$E_ARN" ] || fail "regions" "aws.east's object did not survive the delete in the other region: got [$STILL]"
echo "us-west-2: $LG_NAME is gone; us-east-1: $STILL still there, same name" | evidence

if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control. The assertion below is that aws.west's dead instance" \
    "is NAMED, and #745's symptom was silence - so the direction that has" \
    "to be provable is that this run CAN report the dead instance as" \
    "unchanged. Two changes manufacture exactly that world. East's object" \
    "loses its ownership markers, because an unmarked sighting is the only" \
    "shape the cache's envelope-vouch arm consumes. And aws.west is" \
    "pointed at us-east-1, so the west pass lists the region east's" \
    "object lives in and sights the same client-chosen name. The record" \
    "still attests west's identity, so the cache serves west's dead" \
    "instance and the plan reports it unchanged. If the check below is" \
    "scenery, this run passes it anyway."
  cmd "aws logs untag-resource (east) ; point aws.west at us-east-1 ; choudoufu plan -refresh=false"
  awsl --region us-east-1 logs untag-resource --resource-arn "$E_ARN" --tag-keys tofu-estate tofu-address \
    || fail "regions" "BREAK: could not strip aws.east's markers"
  write_estate us-east-1
  B_OUT="$(plan_rf break)" || fail "regions" "BREAK: the plan failed outright instead of reporting a stale answer: $B_OUT"
  if grep -q 'aws_cloudwatch_log_group.west will be created' <<< "$B_OUT"; then
    fail "regions" "BREAK: aws.west's dead instance was still named, so this control manufactured nothing and step 2's check proves nothing: $B_OUT"
  fi
  grep 'state cache hit for aws_cloudwatch_log_group.west' "$LOGDIR/break.log" | sed 's/.*projection: //' | head -1 | evidence
  grep -E 'Plan: |No changes\.' <<< "$B_OUT" | head -1 | evidence
  proof "caught - with aws.west pointed at the region east's same-named object lives in, the deleted us-west-2 instance reads as unchanged and no plan line mentions it. Step 2's check is what stands between that and a silent, dead instance."
  exit 0
fi

P_DEL="$(plan_rf delete)" || fail "regions" "the post-delete -refresh=false plan failed"
grep -q 'aws_cloudwatch_log_group.west will be created' <<< "$P_DEL" \
  || fail "regions" "aws.west's deleted instance ($LG_NAME in us-west-2) did not surface - aws.east's same-named object answered for it, which is the #745 defect: $P_DEL"
grep -qE 'Plan: 1 to add, 0 to change, 0 to destroy' <<< "$P_DEL" \
  || fail "regions" "the plan proposes more than aws.west's one instance: $P_DEL"
if grep -q 'aws_cloudwatch_log_group.east will be' <<< "$P_DEL"; then
  fail "regions" "aws.east's instance was dragged into a change by a delete in aws.west's region: $P_DEL"
fi
HITS_EAST="$(grep -c 'state cache hit for aws_vpc.east' "$LOGDIR/delete.log" || true)"
[ "$HITS_EAST" -gt 0 ] \
  || fail "regions" "aws.east's converged instances stopped being served from the cache because something was deleted in aws.west's region: $(grep 'state cache hit' "$LOGDIR/delete.log" || echo 'no cache hits at all')"
grep -E 'aws_cloudwatch_log_group.west will be created' <<< "$P_DEL" | head -1 | evidence
grep -E 'Plan: 1 to add' <<< "$P_DEL" | head -1 | evidence
grep 'state cache hit for aws_vpc.east' "$LOGDIR/delete.log" | sed 's/.*projection: //' | head -1 | evidence
proof "aws.west (us-west-2) lost $LG_NAME and the plan named that instance, and only that instance; aws.east's identical name in us-east-1 never answered for it, and aws.east's own instances stayed served from the cache."

explain \
  "One ordinary apply puts aws.west's half back. Nothing about the" \
  "recovery is region-specific: the run re-creates what its own provider" \
  "configuration could not find."
cmd "choudoufu apply -auto-approve"
A2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "regions" "the reconverging apply failed: $A2"
grep -qE 'Resources: 1 added, 0 changed, 0 destroyed' <<< "$A2" \
  || fail "regions" "reconvergence was not exactly one create in aws.west's region: $A2"
proof "one create in us-west-2, nothing in us-east-1 - the estate is whole again."

step "3. an orphan in a region nothing declares any more"
explain \
  "Question four, and the answer is the unflattering one, so it is" \
  "pinned rather than argued. The sweep looks wherever a provider" \
  "configuration points. Drop aws_vpc.west's block while aws.west is" \
  "still configured for something else, and the sweep still lists" \
  "us-west-2 and names the orphan there. Drop aws.west's LAST" \
  "declaration and the provider configuration goes with it: nothing" \
  "points at us-west-2 any more, the sweep stops looking, and the marked" \
  "objects sit there with no run proposing to remove them. The unit of" \
  "the sweep is the provider configuration, never the region."
cmd "remove aws_vpc.west ; plan ; remove the rest of aws.west ; plan"
drop_block 'resource "aws_vpc" "west"'
P_ORPH="$(plan_default)" || fail "regions" "the orphan plan failed"
grep -q 'aws_vpc.west will be destroyed' <<< "$P_ORPH" \
  || fail "regions" "aws.west's undeclared vpc was not named while aws.west is still a configured provider: $P_ORPH"
grep -E 'aws_vpc.west will be destroyed' <<< "$P_ORPH" | head -1 | evidence
drop_block 'resource "aws_cloudwatch_log_group" "west"'
P_NOPROV="$(plan_default)" || fail "regions" "the plan with no west declarations failed"
grep -q "No changes." <<< "$P_NOPROV" \
  || fail "regions" "expected the west region to drop out of the sweep with its last declaration, but the plan proposed something: $P_NOPROV"
LEFT="$(awsl --region us-west-2 ec2 describe-vpcs --filters "Name=tag:tofu-address,Values=aws_vpc.west" --query 'Vpcs[0].VpcId' --output text)"
[ -n "$LEFT" ] && [ "$LEFT" != "None" ] \
  || fail "regions" "the us-west-2 vpc is gone, so 'the sweep stopped looking' is not what was measured here"
echo "with no aws.west declaration left: $(grep -m1 'No changes.' <<< "$P_NOPROV")" | evidence
echo "and $LEFT is still in us-west-2, still marked tofu-address=aws_vpc.west" | evidence
proof "an orphan in a region a provider configuration still points at is named in that region; a region nothing declares any more drops out of the sweep and keeps its marked objects. Recovering them is putting a provider configuration for that region back - which the next step does."

step "4. teardown"
explain \
  "Both provider configurations come back, the estate binds both regions" \
  "again from the markers alone, and one destroy removes exactly what" \
  "the two regions hold between them."
cmd "restore both provider configurations ; choudoufu apply -destroy -auto-approve"
write_estate
P_BACK="$(plan_default)" || fail "regions" "the restored plan failed"
grep -q "No changes." <<< "$P_BACK" \
  || fail "regions" "putting aws.west back did not rebind its region from markers: $P_BACK"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "regions" "teardown failed: $DOUT"
grep -qE "Resources: 0 added, 0 changed, $ADDED destroyed" <<< "$DOUT" \
  || fail "regions" "teardown did not remove exactly $ADDED resources across both regions: $DOUT"
proof "$ADDED destroyed across aws.east and aws.west - both regions gone, from one command."

echo "  What you watched: one estate spanning two provider configurations,"
echo "  the same estate marker in both regions and one record store behind"
echo "  them; a client-chosen name mirrored into two regions staying two"
echo "  distinct objects, so deleting one named that region's instance and"
echo "  only that one while the other region kept being served from cache;"
echo "  each pass listing its own configuration's declarations, so an"
echo "  account-global type declared once was listed once; and the honest"
echo "  edge - the sweep follows provider configurations, so a region's"
echo "  last declaration takes the region out of the sweep with it."
