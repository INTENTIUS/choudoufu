# the-boundary-holds-across-regions
# CLAIM 16 - The boundary holds across provider configurations: one estate spans regions, and every answer a plan gives is about one provider configuration. ~2 min.

SMOKE_WORK="$SMOKE_WORKROOT/tworegions"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
LOGDIR="$SMOKE_WORKROOT/tworegions-logs"; mkdir -p "$LOGDIR"

LG_NAME="/smoke-two-regions/app"
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

# set_provider_change writes (or removes) the strict toggle GitHub issue
# #906's escape hatch lives in, inside the live block write_estate wrote.
# It edits rather than re-writing the estate so that the resource blocks -
# including the repoint step 5 has already made - stay exactly as they are.
set_provider_change() {
  python3 - "$SMOKE_WORK/main.tf" "${1:-}" <<'PCEOF'
import re, sys
path, setting = sys.argv[1], sys.argv[2]
s = open(path).read()
s = re.sub(r'\n    strict \{\n      provider_change = "[^"]*"\n    \}\n', '\n', s)
if setting:
    s = s.replace('    estate = "smoke-two-regions"\n',
                  '    estate = "smoke-two-regions"\n\n    strict {\n      provider_change = "%s"\n    }\n' % setting, 1)
open(path, 'w').write(s)
PCEOF
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
  "And the honest edges: the sweep looks where a provider configuration" \
  "points, so removing a region's last declaration takes the region out" \
  "of the sweep with it, and repointing a block at another region is a" \
  "replace whose other half no address can express - so it is refused" \
  "by name rather than half-done - unless the estate says otherwise" \
  "with strict { provider_change = \"recreate\" }, and then it is" \
  "permitted by name instead."

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
  { echo "the whole change set this plan proposes:"; grep -E '^  # .* will be ' <<< "$B_OUT" || echo "  (nothing)"; } | evidence
  echo "aws_cloudwatch_log_group.west is not in it, and its object in us-west-2 is gone" | evidence
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
# aws_cloudwatch_log_group.east is CONCRETE (client-named, not
# needs-discovery): its tag-index vouch rides discovery.Result's
# VerifiedDeclared, which Merge did not carry into a two-pass estate until
# issue #905 - so this instance's cache hit is the fix's own proof, in the
# same two-pass estate #745's step already stands up, not a synthetic one.
HITS_LG_EAST="$(grep -c 'state cache hit for aws_cloudwatch_log_group.east' "$LOGDIR/delete.log" || true)"
[ "$HITS_LG_EAST" -gt 0 ] \
  || fail "regions" "aws.east's CONCRETE log group stopped being served from the cache (issue #905) because something was deleted in aws.west's region: $(grep 'state cache hit' "$LOGDIR/delete.log" || echo 'no cache hits at all')"
grep -E 'aws_cloudwatch_log_group.west will be created' <<< "$P_DEL" | head -1 | evidence
grep -E 'Plan: 1 to add' <<< "$P_DEL" | head -1 | evidence
grep 'state cache hit for aws_vpc.east' "$LOGDIR/delete.log" | sed 's/.*projection: //' | head -1 | evidence
grep 'state cache hit for aws_cloudwatch_log_group.east' "$LOGDIR/delete.log" | sed 's/.*projection: //' | head -1 | evidence
proof "aws.west (us-west-2) lost $LG_NAME and the plan named that instance, and only that instance; aws.east's identical name in us-east-1 never answered for it, and aws.east's own instances - the needs-discovery vpc AND the CONCRETE log group alike - stayed served from the cache."

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
write_estate
P_BACK="$(plan_default)" || fail "regions" "the restored plan failed"
grep -q "No changes." <<< "$P_BACK" \
  || fail "regions" "putting aws.west back did not rebind its region from markers: $P_BACK"
proof "an orphan in a region a provider configuration still points at is named in that region; a region nothing declares any more drops out of the sweep and keeps its marked objects. Recovering them is putting a provider configuration for that region back, and doing so rebound us-west-2 from its markers with nothing else."

step "4. recovery is a re-run in both regions at once"
explain \
  "Claim 5 with two provider configurations. Every local file the run" \
  "wrote is deleted - the state cache and the whole record store - and" \
  "the same plan runs again. Both regions have to come back from what" \
  "the cloud itself carries: the markers in each region for the" \
  "server-assigned instances, the configuration's own names for the" \
  "client-named ones. If recovery were region-blind, the region whose" \
  "objects were not re-derived would show up as a create. The ANSWER" \
  "must be identical; the WORK must not be, because with no record to" \
  "read the run has to list for markers in each region - and a run that" \
  "reported identical coverage would be a run that never noticed the" \
  "files were gone."
cmd "rm the cache and the record store ; choudoufu plan"
[ -f "$CACHE" ] || fail "regions" "no state cache to lose - this step would measure nothing"
rm -f "$CACHE"
rm -rf "$SMOKE_WORK/.tofu-records"
P_LOST="$(plan_default)" || fail "regions" "the plan after losing every local file failed"
CS_BACK="$(grep -E '^No changes\.|^Plan: |^  # .* will be ' <<< "$P_BACK" || true)"
CS_LOST="$(grep -E '^No changes\.|^Plan: |^  # .* will be ' <<< "$P_LOST" || true)"
[ -n "$CS_LOST" ] || fail "regions" "the post-recovery plan printed no change set at all, so nothing below compares anything"
[ "$CS_BACK" = "$CS_LOST" ] \
  || fail "regions" "losing the cache and the record store changed the answer for a two-region estate.
--- before ---
$CS_BACK
--- after ---
$CS_LOST"
[ "$P_BACK" != "$P_LOST" ] \
  || fail "regions" "the whole plan text was identical, coverage report included - the run did not notice the record store was gone, so this step measured nothing"
echo "change set before losing the files: $CS_BACK" | evidence
echo "change set after:                   $CS_LOST" | evidence
echo "coverage report differs: aws_vpc had to be listed per region for its markers, which is the price of the loss" | evidence
proof "the answer survived losing every local file - both aws.east's and aws.west's halves re-derived from what their own regions carry - and the only thing that moved was the work each provider configuration had to do."

step "5. a region change is a replace: refused by default, permitted by name"
explain \
  "The last question, answered by measurement rather than hope. An" \
  "address's provider configuration is part of what the address means," \
  "so pointing a resource at a different region cannot relocate an" \
  "object - no cloud API moves a VPC between regions, and live-mv" \
  "rewrites ownership tags rather than resources. So a region change is" \
  "a replace, and only half of it is expressible: a resource address" \
  "carries exactly one provider configuration in the plan graph, taken" \
  "from its own block, so the destroy of the object left behind in the" \
  "region that block no longer names cannot be planned at that address" \
  "at all. What this step pins is what the run does with the half it" \
  "cannot plan, in both of the two answers the schema offers. By" \
  "default it refuses - naming the live VPC, the region it is in, the" \
  "region the address now points at, and what an operator can do about" \
  "it - because creating the new object beside a live marked one would" \
  "leave two live resources carrying this estate's marker for one" \
  "address, which live/MARKERS.md forbids. An estate that meant it says" \
  "so with strict { provider_change = \"recreate\" }, and then the plan" \
  "proceeds and the same finding is a warning instead. What the toggle" \
  "buys is the create; it does not buy silence, and in neither mode" \
  "does anything promise that marker discovery will find the old" \
  "object - the sentence that sent operators looking in the wrong" \
  "region. That is issue #906 and its maintainer ruling."
cmd "aws_vpc.west: provider = aws.east ; choudoufu plan ; aws ec2 describe-vpcs (us-west-2)"
OLD_VPC="$(awsl --region us-west-2 ec2 describe-vpcs --filters "Name=tag:tofu-address,Values=aws_vpc.west" --query 'Vpcs[0].VpcId' --output text)"
[ -n "$OLD_VPC" ] && [ "$OLD_VPC" != "None" ] || fail "regions" "no marked vpc in us-west-2 to move away from"
sed_i "$SMOKE_WORK/main.tf" '/resource "aws_vpc" "west"/,/^}/ s/provider   = aws.west/provider   = aws.east/'
grep -A1 'resource "aws_vpc" "west"' "$SMOKE_WORK/main.tf" | grep -q 'aws.east' \
  || fail "regions" "the edit did not repoint aws_vpc.west at aws.east"
# The plan is expected to fail now, so its status is not the measurement -
# the verdict lines below are. || true keeps set -e out of the way.
P_MOVE="$(plan_default || true)"
grep -q 'Marked resource outside its address' <<< "$P_MOVE" \
  || fail "regions" "a region change did not refuse: the old region's marked object was abandoned with no line about it, which is issue #906's defect: $P_MOVE"
grep -q "$OLD_VPC" <<< "$P_MOVE" \
  || fail "regions" "the refusal does not name $OLD_VPC, the live object left in us-west-2, so an operator cannot act on it: $P_MOVE"
grep -q 'us-west-2' <<< "$P_MOVE" \
  || fail "regions" "the refusal does not name the region the object is in: $P_MOVE"
grep -q 'provider_change = "recreate"' <<< "$P_MOVE" \
  || fail "regions" "the refusal does not name the toggle that permits it, so it refuses without saying how to proceed: $P_MOVE"
if grep -q 'aws_vpc.west will be created' <<< "$P_MOVE"; then
  fail "regions" "the run proposed creating aws_vpc.west in us-east-1 anyway, so it is about to manufacture two live resources carrying one address's marker: $P_MOVE"
fi
# The sentence is line-wrapped by the diagnostic renderer, so it is only
# findable once newlines AND the wrap indent are folded to single spaces.
# Without the -s the grep can never match and this check would be scenery.
if tr '\n' ' ' <<< "$P_MOVE" | tr -s ' ' | grep -q 'Marker discovery will find it'; then
  fail "regions" "the plan still tells the operator marker discovery will find aws_vpc.west; discovery for it lists us-east-1 now, where the object is not and cannot be (issue #906)"
fi
STILL_VPC="$(awsl --region us-west-2 ec2 describe-vpcs --filters "Name=tag:tofu-address,Values=aws_vpc.west" --query 'Vpcs[0].VpcId' --output text)"
[ "$STILL_VPC" = "$OLD_VPC" ] \
  || fail "regions" "the us-west-2 object is not where this step left it ($STILL_VPC vs $OLD_VPC) - a refusal must change nothing in the cloud"
grep -A6 'Marked resource outside its address' <<< "$P_MOVE" | head -8 | evidence
echo "and $OLD_VPC is still in us-west-2, untouched: a refusal is loud and reversible" | evidence
proof "the default refused by name - $OLD_VPC, in us-west-2, for an address that now points at us-east-1 - rather than creating a second object and leaving the first one billed and unmentioned, and it named the toggle that permits it."

explain \
  "The same repoint, with the escape hatch set. The plan proceeds and" \
  "creates aws_vpc.west in us-east-1, and the same finding comes back" \
  "as a warning that names $OLD_VPC, says where it is, and says that" \
  "nothing will ever propose anything for it. That warning is the only" \
  "notice there will be, which is why the toggle is worth its own" \
  "assertion rather than just an absence of the refusal."
cmd "strict { provider_change = \"recreate\" } ; choudoufu plan"
set_provider_change recreate
grep -q 'provider_change = "recreate"' "$SMOKE_WORK/main.tf" \
  || fail "regions" "the toggle was not written into the estate's live block"
P_ALLOW="$(plan_default)" || fail "regions" "the plan under the toggle failed instead of proceeding: $P_ALLOW"
grep -q 'aws_vpc.west will be created' <<< "$P_ALLOW" \
  || fail "regions" "the toggle is set and the create was still not proposed, so it bought nothing: $P_ALLOW"
grep -q 'Marked resource abandoned by a provider configuration change' <<< "$P_ALLOW" \
  || fail "regions" "the toggle silenced the finding; the abandoned object must be named on every plan that sees it: $P_ALLOW"
grep -q "$OLD_VPC" <<< "$P_ALLOW" \
  || fail "regions" "the warning does not name $OLD_VPC, so it is not the notice it has to be: $P_ALLOW"
if tr '\n' ' ' <<< "$P_ALLOW" | tr -s ' ' | grep -q 'Marker discovery will find it'; then
  fail "regions" "the coverage line still promises marker discovery will find aws_vpc.west, in a region the object is not in (issue #906)"
fi
grep -A6 'Warning: Marked resource abandoned' <<< "$P_ALLOW" | head -8 | evidence
grep -E '^Plan: ' <<< "$P_ALLOW" | head -1 | evidence
proof "with strict { provider_change = \"recreate\" } the plan proceeds - one create in us-east-1 - and the same finding comes back as a warning naming $OLD_VPC and saying nothing will propose anything for it. The toggle buys the create, not the silence."

set_provider_change ""
sed_i "$SMOKE_WORK/main.tf" '/resource "aws_vpc" "west"/,/^}/ s/provider   = aws.east/provider   = aws.west/'
P_UNDO="$(plan_default)" || fail "regions" "the plan after undoing the region change failed"
grep -q "No changes." <<< "$P_UNDO" \
  || fail "regions" "undoing the region change did not return the estate to converged: $P_UNDO"
grep -m1 'No changes.' <<< "$P_UNDO" | evidence
proof "and pointing the block back at aws.west, with the toggle removed again, planned clean: nothing either mode did changed anything in either region."

step "6. teardown"
explain \
  "One destroy removes exactly what the two provider configurations" \
  "hold between them."
cmd "choudoufu apply -destroy -auto-approve"
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
echo "  account-global type declared once was listed once; the honest"
echo "  edge - the sweep follows provider configurations, so a region's"
echo "  last declaration takes the region out of the sweep with it; and"
echo "  a region change refused by name rather than half-planned, because"
echo "  a create in the new region beside a live marked object in the old"
echo "  one is two live resources answering to one address - and the same"
echo "  repoint permitted, still by name, once the estate asks for it with"
echo "  strict { provider_change = \"recreate\" }."
