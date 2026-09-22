# a-killed-apply-hides-nothing
# CLAIM 42 - A killed apply hides nothing it marked: after a SIGKILL mid-apply every marked object is named by the next plan and bound by the re-run with no duplicate, and the two windows in which something can still be hidden - the marker write that follows a tag_on_create=false create, and the record written after the walk - are measured here rather than assumed. ~4 min.

SMOKE_WORK="$SMOKE_WORKROOT/killed"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-killed-apply"
ZONE="killed-apply.example."
CIDR="10.91.0.0/16"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
RECORDS="$SMOKE_WORK/.tofu-records"
EFFECTS="$SMOKE_WORK/effects.log"

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

# Four resources in one dependency chain, so a single kill lands inside all
# three windows this claim is about:
#
#   terraform_data.effect  record-carried. It has no cloud home, so its
#                          record is the only trace that it ran, and
#                          projection.WriteBack writes that once, after the
#                          whole graph walk (internal/backend/local/
#                          backend_apply.go). Its provisioner appends a line
#                          to effects.log, so "it ran" is counted rather
#                          than assumed.
#   aws_vpc.main           marker-carried the ordinary way: internal/live/
#                          stamp puts the markers in the create request, so
#                          the object is marked the instant it exists and
#                          there is no window at all.
#   aws_route53_zone.dns   marker-carried, and the one type on the
#                          registry's tag_on_create=false list that the
#                          reference estate uses. CreateHostedZone takes no
#                          Tags parameter, so this fork withholds the
#                          markers from the create call and writes them
#                          itself once ApplyResourceChange returns (#1084,
#                          #1512). So the zone is in the account from the
#                          create call and the markers land only when the
#                          provider's whole create step returns: measured
#                          against this emulator at 15.0s apart, which is
#                          what makes the window wide enough to kill inside
#                          on purpose rather than by luck.
#   terraform_data.tail    the work after the zone. Without it the apply is
#                          over before a poll of the account can see the
#                          zone, and the kill would land by luck. The re-run
#                          pays for its sleep once.
cat > "$SMOKE_WORK/main.tf" <<TFEOF
terraform {
  live {
    estate = "$ESTATE"
  }
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
}

resource "terraform_data" "effect" {
  input = "killed-apply"

  provisioner "local-exec" {
    command = "echo ran >> effects.log"
  }
}

resource "aws_vpc" "main" {
  cidr_block = "$CIDR"
  depends_on = [terraform_data.effect]
}

resource "aws_route53_zone" "dns" {
  name       = "$ZONE"
  depends_on = [aws_vpc.main]
}

resource "terraform_data" "tail" {
  input      = "tail"
  depends_on = [aws_route53_zone.dns]

  provisioner "local-exec" {
    command = "sleep 25"
  }
}
TFEOF

# The reads below all run while a real apply is being killed underneath
# them, so each returns a value and never a failure: an empty answer is a
# fact this scenario reports, not a reason to stop.
#
# zones_named is the pin. The kill fires on a count read off the live
# account - hosted zones named $ZONE, 0 then 1 - never on a timer, so it
# lands at the same point of the walk whether the machine is fast or loaded.
zones_named() {
  local out
  out="$(awsl route53 list-hosted-zones --query "length(HostedZones[?Name=='$ZONE'])" --output text 2>/dev/null)" || out=""
  printf '%s' "${out:-0}"
}
zone_ids() {
  local out
  out="$(awsl route53 list-hosted-zones --query "HostedZones[?Name=='$ZONE'].Id" --output text 2>/dev/null)" || out=""
  printf '%s' "$out" | tr '\t' '\n' | sed 's|/hostedzone/||' | sed '/^$/d'
}
# zone_markers prints the ownership markers Route 53 itself holds for one
# zone. Since lex00/floci#215 the Tagging API writes the service's own tags,
# so this is the same pair the estate-wide sweep reads.
zone_markers() {
  local out
  out="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$1" --query "ResourceTagSet.Tags[?Key=='tofu-estate'||Key=='tofu-address'].Key" --output text 2>/dev/null)" || out=""
  printf '%s' "$out" | tr '\t' ' ' | tr -s ' '
}
vpcs_named() {
  local out
  out="$(awsl ec2 describe-vpcs --filters "Name=cidr,Values=$CIDR" --query 'length(Vpcs)' --output text 2>/dev/null)" || out=""
  printf '%s' "${out:-0}"
}
vpc_marked() {
  local out
  out="$(awsl ec2 describe-vpcs --filters "Name=cidr,Values=$CIDR" "Name=tag:tofu-estate,Values=$ESTATE" "Name=tag:tofu-address,Values=aws_vpc.main" --query 'Vpcs[].VpcId' --output text 2>/dev/null)" || out=""
  printf '%s' "${out%%[[:space:]]*}"
}
# swept_arns is the estate-wide sweep's own read: every ARN in the account
# carrying this estate's tag.
swept_arns() {
  local out
  out="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null)" || out=""
  printf '%s' "$out" | tr '\t' '\n' | sed '/^$/d'
}
lines_in() { if [ -f "$1" ]; then wc -l < "$1" | tr -d ' '; else echo 0; fi; }
# record_key is how projection.RecordKey names an instance's record:
# unpadded URL-safe base64 of the address, under a directory named for the
# type. Derived here rather than pasted, so a record this scenario says is
# absent is the record of the instance it names.
record_key() { python3 -c "import base64,sys;print(base64.urlsafe_b64encode(sys.argv[1].encode()).decode().rstrip('='))" "$1"; }
records_for() {
  if [ -d "$RECORDS" ]; then find "$RECORDS" -type f -name "$(record_key "$1")" | wc -l | tr -d ' '; else echo 0; fi
}
record_paths() {
  if [ -d "$RECORDS" ]; then ( cd "$SMOKE_WORK" && find .tofu-records -type f | sort | tr '\n' ' ' ); else printf 'nothing'; fi
}

step "the claim"
explain \
  "Claims 1 and 5 cover the crash shape, but both manufacture it with the" \
  "AWS CLI: a resource created and tagged by hand, standing in for one a" \
  "dead apply left behind. Nothing kills a real apply. This does, with" \
  "SIGKILL, mid-walk, at a point pinned by a count read off the account." \
  "" \
  "Three things are created before the kill and each answers a different" \
  "question. A VPC, whose markers ride its create call, so there is no" \
  "window at all. A hosted zone, whose type reads tag_on_create false in" \
  "live/registry.json - the create call cannot carry tags, so this fork" \
  "writes them once the provider's create step returns (#1084, #1512)." \
  "And a record-carried terraform_data, whose record is written after the" \
  "whole walk. The claim is about what the next plan can name, and the" \
  "answer is not the same for the three."

step "1. init, and start the apply that is going to be killed"
cmd "choudoufu init && choudoufu apply -auto-approve   # to be killed"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "killed" "init failed"
[ "$(zones_named)" = "0" ] || fail "killed" "the account already holds a zone named $ZONE before anything ran"
( cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color > "$SMOKE_WORK/apply-1.log" 2>&1 ) &
APPLY_PID=$!
note "started, pid $APPLY_PID"

step "2. kill it with SIGKILL, pinned by the account's own count"
explain \
  "The pin is a count read off the live account: hosted zones named" \
  "$ZONE, polled as fast as the CLI answers, 0 then 1. The instant it is 1" \
  "the apply is killed - the process and every child of it, so no provider" \
  "plugin outlives the run. A timer would pin a different point of the" \
  "walk on every machine, and a different point is a different answer."
cmd "while [ \"\$(aws route53 list-hosted-zones ...)\" = 0 ]; do :; done ; kill -KILL"
SAW=0
DEADLINE=$(( $(date +%s) + 240 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  if [ "$(zones_named)" != "0" ]; then SAW=1; break; fi
  kill -0 "$APPLY_PID" 2>/dev/null || break
done
if [ "$SAW" != "1" ]; then
  tail -20 "$SMOKE_WORK/apply-1.log" | evidence || true
  fail "killed" "the zone never appeared in the account while the apply was running, so nothing was killed mid-walk (the apply's last lines are above)"
fi
KIDS="$(smoke_descendants "$APPLY_PID")" || KIDS=""
# shellcheck disable=SC2086  # a list of pids, split on purpose
kill -KILL "$APPLY_PID" $KIDS 2>/dev/null || true
APPLY_RC=0
wait "$APPLY_PID" 2>/dev/null || APPLY_RC=$?
KILLED_ZONE="$(zone_ids | head -1)" || KILLED_ZONE=""
ZONE_MARKERS_AT_KILL="$(zone_markers "$KILLED_ZONE")" || ZONE_MARKERS_AT_KILL=""
KILLED_VPC="$(vpc_marked)" || KILLED_VPC=""
echo "apply exited $APPLY_RC; its last line: $(tail -1 "$SMOKE_WORK/apply-1.log")" | evidence
if grep -q 'Apply complete' "$SMOKE_WORK/apply-1.log"; then
  fail "killed" "the apply finished before it could be killed; there is no mid-walk state to measure"
fi
[ "$APPLY_RC" != "0" ] || fail "killed" "the killed apply exited 0"
proof "the apply is dead, killed the moment the account held one zone named $ZONE."

step "3. what the cloud holds, and what the run kept"
explain \
  "Four reads, none of them through choudoufu. The VPC and whether it" \
  "carries its markers. The zone and the same question. effects.log, which" \
  "the record-carried instance's provisioner appends a line to. And what" \
  "is on disk: the record store, and the state cache, which is written" \
  "after the walk like the records and so is not there at all."
cmd "aws ec2 describe-vpcs ; aws route53 list-tags-for-resource ; ls .tofu-records ; ls .terraform"
echo "vpcs with cidr $CIDR: $(vpcs_named)   marked as aws_vpc.main: ${KILLED_VPC:-none}" | evidence
echo "hosted zones named $ZONE: $(zones_named) (id ${KILLED_ZONE:-none})   its markers: ${ZONE_MARKERS_AT_KILL:-none}" | evidence
echo "the estate's tagged ARNs, as the sweep reads them: $(swept_arns | tr '\n' ' ')" | evidence
EFFECT_RUNS="$(lines_in "$EFFECTS")"
echo "effects.log lines (times the record-carried effect has run): $EFFECT_RUNS" | evidence
echo "what the record store holds: $(record_paths)" | evidence
echo "state cache written: $([ -f "$CACHE" ] && echo yes || echo no)" | evidence
[ -n "$KILLED_VPC" ] || fail "killed" "the VPC the killed apply created carries no marker; internal/live/stamp puts them in the create request, so it is marked the instant it exists or this claim's first leg is gone"
[ "$EFFECT_RUNS" = "1" ] || fail "killed" "the record-carried effect ran $EFFECT_RUNS times, not once, so the kill did not land where this claim says it did"
[ "$(records_for terraform_data.effect)" = "0" ] \
  || fail "killed" "the killed run wrote a record for terraform_data.effect; projection.WriteBack runs after the whole walk, so a killed walk writes none - if this has changed, the record window this claim measures has moved"
[ ! -f "$CACHE" ] || fail "killed" "the killed run wrote a state cache, which is written after the walk"
if [ -n "$ZONE_MARKERS_AT_KILL" ]; then
  proof "the VPC is marked and so is the zone: the kill landed after the tag write that follows a tag_on_create=false create, and everything in the account belongs to the estate. The record-carried effect HAS run and no record says so."
else
  proof "the VPC is marked, and the zone is NOT: the kill landed inside the window between CreateHostedZone and the marker write that follows the provider's create step. The sweep lists the VPC and cannot see the zone. The record-carried effect has run and no record says so."
fi

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - take the markers off what the killed apply created"
  explain \
    "Everything below rests on the markers the dead apply wrote. So this" \
    "arm strips them - from the Tagging API the sweep reads and from the" \
    "EC2 tags the VPC carries - leaving the same objects in the same" \
    "account with no ownership on them. The re-run must then be unable to" \
    "bind and must propose building its own, which is stock's behaviour" \
    "and the reason stock's crash window leaks. If it binds anyway, every" \
    "assertion in steps 4 and 5 is scenery."
  cmd "aws ec2 delete-tags ; aws resourcegroupstaggingapi untag-resources --tag-keys tofu-estate tofu-address"
  for a in $(swept_arns); do
    awsl resourcegroupstaggingapi untag-resources --resource-arn-list "$a" --tag-keys tofu-estate tofu-address >/dev/null 2>&1 || true
  done
  awsl ec2 delete-tags --resources "$KILLED_VPC" --tags Key=tofu-estate Key=tofu-address >/dev/null 2>&1 || true
  STILL="$(swept_arns | tr '\n' ' ')"
  [ -z "$STILL" ] || fail "killed" "BREAK: the markers are still on $STILL, so this control broke nothing and proved nothing"
  [ -z "$(vpc_marked)" ] || fail "killed" "BREAK: the VPC still carries its markers, so this control broke nothing"
  echo "the estate's tagged ARNs after the strip: none" | evidence
  BPLAN="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "killed" "BREAK: the plan over the stripped account failed: $BPLAN"
  { grep -E 'will be created|^Plan:' <<< "$BPLAN" || true; } | evidence
  grep -q 'aws_vpc.main will be created' <<< "$BPLAN" \
    || fail "killed" "BREAK: the plan did not propose creating the VPC, so it bound an UNMARKED object and the bind assertions in steps 4 and 5 are guessing: $BPLAN"
  BAPPLY_RC=0
  BAPPLY="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || BAPPLY_RC=$?
  BVPCS="$(vpcs_named)"
  echo "vpcs with cidr $CIDR after the re-run: $BVPCS (apply exited $BAPPLY_RC)" | evidence
  if [ "$BAPPLY_RC" = "0" ] && [ "$BVPCS" = "1" ]; then
    fail "killed" "BREAK: the re-run left one VPC and exited 0 over an UNMARKED account - it bound to something it cannot prove it owns: $BAPPLY"
  fi
  proof "caught - with the markers gone the re-run could not bind: it proposed the VPC as a create and the account now holds $BVPCS with this estate's cidr. The binding in steps 4 and 5 rests on the markers the dead apply wrote, which is exactly the boundary the claim states."
  ( cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "4. the next plan names what the killed apply marked"
explain \
  "No import, no state surgery, no recovery mode: the next ordinary plan." \
  "The VPC is read back from its own markers and proposed for nothing. The" \
  "record-carried instance is named as a create, because nothing recorded" \
  "that it ran - the honest answer, said out loud rather than left to be" \
  "discovered. What the plan says about the zone is the measurement this" \
  "claim exists for, and it follows from whether the kill beat the marker."
cmd "choudoufu plan"
PLAN="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "killed" "the plan after the kill failed: $PLAN"
{ grep -E 'will be created|^Plan:' <<< "$PLAN" || true; } | evidence
grep -q 'Plan: [0-9]* to add, 0 to change, 0 to destroy' <<< "$PLAN" \
  || fail "killed" "the plan after the kill proposes a change or a destroy; nothing the dead apply left may be rebuilt or removed: $PLAN"
if grep -q 'aws_vpc.main will be created' <<< "$PLAN"; then
  fail "killed" "the plan proposes creating a VPC the account already holds under this estate's markers - the re-run would duplicate it: $PLAN"
fi
grep -q 'terraform_data.effect will be created' <<< "$PLAN" \
  || fail "killed" "the record-carried instance is not named by the plan at all: $PLAN"
proof "the marked VPC is not in the plan, because there is nothing to do to it: the dead apply's markers are the whole recovery. The record-carried instance is named as a create - its effect has already run once, and the run says what it can prove rather than what happened."
if [ -n "$ZONE_MARKERS_AT_KILL" ]; then
  if grep -q 'aws_route53_zone.dns will be created' <<< "$PLAN"; then
    fail "killed" "the zone carries this estate's markers and the plan still proposes creating one: $PLAN"
  fi
  proof "the zone was marked before the kill and is bound the same way the VPC is."
else
  grep -q 'aws_route53_zone.dns will be created' <<< "$PLAN" \
    || fail "killed" "the zone carries no marker and the plan proposes no zone either, so this run accounts for it not at all: $PLAN"
  proof "and here is the bound. The zone the dead apply created carries no marker, so no run can prove it is this estate's, and the plan proposes a second one. That is the window #1084 named. It opens when CreateHostedZone returns and closes when the marker write lands, which is after the provider's WHOLE create step - measured against this emulator at 15.0s, the zone visible in the account the whole time and the apply reporting \"Creation complete after 15s\". The marker write itself is one round trip; the window is not."
fi

step "5. the re-run: what it binds, and what it duplicates"
explain \
  "The whole recovery is the same apply, run again. What the dead apply" \
  "marked is bound and not rebuilt. What it did not mark is duplicated," \
  "because a duplicate is the only honest thing a tool can do with an" \
  "object nobody can prove is theirs - that is stock's behaviour for" \
  "EVERY resource in a crashed apply, and here it is the residue of one" \
  "window on ten types. The record-carried effect runs a second time, for" \
  "the same reason: at-least-once is the bound, not a footnote."
cmd "choudoufu apply -auto-approve"
A2_RC=0
A2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || A2_RC=$?
{ grep -E 'Apply complete|Resources: [0-9]+ added|Error:' <<< "$A2" | head -3 || true; } | evidence
[ "$A2_RC" = "0" ] || fail "killed" "the re-run failed: $A2"
VPCS_AFTER="$(vpcs_named)"
ZONES_AFTER="$(zones_named)"
EFFECT_RUNS_AFTER="$(lines_in "$EFFECTS")"
echo "vpcs with cidr $CIDR: $VPCS_AFTER   hosted zones named $ZONE: $ZONES_AFTER   effects.log lines: $EFFECT_RUNS_AFTER" | evidence
echo "what the record store holds now: $(record_paths)" | evidence
[ "$VPCS_AFTER" = "1" ] \
  || fail "killed" "the account holds $VPCS_AFTER VPCs with this estate's cidr; the re-run duplicated the marked one the killed apply created"
[ "$EFFECT_RUNS_AFTER" = "2" ] \
  || fail "killed" "the record-carried effect ran $EFFECT_RUNS_AFTER times in total; this claim's bound is twice - once in the killed run and once in the re-run"
[ "$(records_for terraform_data.effect)" = "1" ] \
  || fail "killed" "the re-run finished and wrote no record for terraform_data.effect, so a third run would run the effect a third time"
if [ -n "$ZONE_MARKERS_AT_KILL" ]; then
  [ "$ZONES_AFTER" = "1" ] || fail "killed" "the zone was marked before the kill and the re-run still left $ZONES_AFTER of them"
  proof "one VPC and one zone: everything the dead apply marked was bound, not rebuilt. The effect ran twice and is now recorded, so it will not run a third time."
else
  [ "$ZONES_AFTER" = "2" ] \
    || fail "killed" "the zone the killed apply left is unmarked and the account holds $ZONES_AFTER of them after the re-run; the residue this claim reports is exactly one orphan and one owned zone"
  proof "one VPC, because it was marked. TWO zones, because one of them was not: the re-run owns the zone it just made and the account still holds the unmarked one. That is the whole cost of the window, and the next step pays it."
fi

step "6. the orphan, and the only surgery in this scenario"
explain \
  "An unmarked object is not this estate's, so nothing in the tool will" \
  "delete it, propose it, or bill for it under this estate's name - the" \
  "sweep behind claim 1 lists what carries the tag. The account is where" \
  "it is found, and the CLI is what removes it. Naming that cost is the" \
  "point: everything else in this run recovered by being re-run, and this" \
  "one thing did not."
ORPHANS=0
if [ -z "$ZONE_MARKERS_AT_KILL" ] && [ -n "$KILLED_ZONE" ]; then
  cmd "aws route53 delete-hosted-zone --id $KILLED_ZONE"
  for z in $(zone_ids); do
    [ "$z" = "$KILLED_ZONE" ] || continue
    [ -z "$(zone_markers "$z")" ] || fail "killed" "the zone the killed apply left has acquired markers since step 3, so it is not the orphan this step is about"
    awsl route53 delete-hosted-zone --id "$z" >/dev/null 2>&1 || fail "killed" "could not delete the orphaned zone $z"
    ORPHANS=$((ORPHANS+1))
  done
  echo "orphans removed by hand: $ORPHANS   hosted zones named $ZONE now: $(zones_named)" | evidence
  [ "$ORPHANS" = "1" ] || fail "killed" "expected exactly one orphaned zone to remove, removed $ORPHANS"
  proof "one object in this whole run needed a human: the zone created inside the marker window. Every other thing the killed apply touched was settled by running the apply again."
else
  echo "no orphan: the kill landed after the zone's marker write" | evidence
  proof "nothing to remove - this run's kill did not land inside the marker window."
fi
P2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "killed" "the plan after the re-run failed: $P2"
grep -E 'No changes.' <<< "$P2" | head -1 | evidence
grep -q 'No changes.' <<< "$P2" || fail "killed" "the estate did not converge: $P2"
proof "the estate converged, from markers and records alone: the state cache the killed run never wrote was never needed."

step "7. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "killed" "teardown failed: $DOUT"
destroyed_exactly "killed" 4 "$DOUT"
proof "four destroyed - the VPC the killed apply created was a full citizen of the estate from the moment it was bound."

echo "  What you watched: a real apply killed with SIGKILL at a point pinned"
echo "  by the account's own resource count, and the three things it left"
echo "  behind measured apart. The VPC was marked in its create call, so the"
echo "  next plan asked nothing of it and the re-run bound it. The"
echo "  record-carried instance had already run its effect with no record"
echo "  saying so, so the plan named it as a create and it ran once more:"
echo "  at-least-once, stated by the run. And the hosted zone - one of ten"
echo "  types whose create call cannot carry a tag - was killed before its"
echo "  marker landed, so nothing could claim it and the re-run built a"
echo "  second one. That window is the one thing here that a re-run does not"
echo "  fix, it is bounded to those ten types, and it is measured above"
echo "  rather than left for a reader to find."
