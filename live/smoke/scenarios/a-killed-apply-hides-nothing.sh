# a-killed-apply-hides-nothing
# CLAIM 5 (aws) - A crash is fixed by re-running. ~4 min.
#
# This proof: A killed apply hides nothing it marked or recorded: after a
# SIGKILL mid-apply every marked object, and every record-carried and
# record-backed instance whose apply step returned, is bound by the re-run
# with no duplicate and no regenerated value. The one window in which
# something can still be hidden - the marker write that follows a
# tag_on_create=false create - is measured here rather than assumed.

SMOKE_WORK="$SMOKE_WORKROOT/killed"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
ESTATE="smoke-killed-apply"
ZONE="killed-apply.example."
CIDR="10.91.0.0/16"
GROUP_NAME="smoke-killed-apply-deploy"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
RECORDS="$SMOKE_WORK/.tofu-records"
EFFECTS="$SMOKE_WORK/effects.log"

# BREAK_EARLY_RECORD=1 is #1944's control: choudoufu rebuilt with the
# mid-apply record write switched off, so every record waits for the end of
# the walk again. Built with go build -overlay, so the source tree is never
# touched; it needs this checkout and Go.
RUN_BIN=""
if [ "${BREAK_EARLY_RECORD:-0}" = "1" ]; then
  [ -z "${CHOUDOUFU_BIN:-}${CHOUDOUFU_VERSION:-}" ] \
    || fail "killed" "BREAK_EARLY_RECORD=1 rebuilds choudoufu from this checkout; it cannot break CHOUDOUFU_BIN or CHOUDOUFU_VERSION. Unset them and run it again with Go installed."
  command -v go >/dev/null 2>&1 || fail "killed" "BREAK_EARLY_RECORD=1 needs Go to build the broken binary"
  SRC="$ROOT/internal/command/live_mode.go"
  mkdir -p "$SMOKE_WORK/break"
  python3 - "$SRC" "$SMOKE_WORK/break/live_mode.go" <<'PYEOF'
import sys
src = open(sys.argv[1]).read()
old = "func (r *liveRunner) WriteInstance(ctx context.Context, state *states.State, addr addrs.AbsResourceInstance, schemas *tofu.Schemas, replaced []addrs.AbsResourceInstance, deposedDestroys []projection.DeposedDestroy) tfdiags.Diagnostics {\n"
assert src.count(old) == 1, "the break patch no longer matches liveRunner.WriteInstance"
open(sys.argv[2], "w").write(src.replace(old, old + "\treturn nil // BREAK_EARLY_RECORD: no record until the walk is over\n"))
PYEOF
  [ -s "$SMOKE_WORK/break/live_mode.go" ] || fail "killed" "the break patch did not apply to $SRC, so this arm would test the real binary"
  printf '{"Replace":{"%s":"%s"}}\n' "$SRC" "$SMOKE_WORK/break/live_mode.go" > "$SMOKE_WORK/break/overlay.json"
  ( cd "$ROOT" && go build -overlay "$SMOKE_WORK/break/overlay.json" -o "$SMOKE_WORK/break/choudoufu" ./cmd/choudoufu ) \
    || fail "killed" "the broken binary did not build"
  RUN_BIN="$SMOKE_WORK/break/choudoufu"
fi
# run_chdf is chdf, or the broken binary under BREAK_EARLY_RECORD.
run_chdf() { if [ -n "$RUN_BIN" ]; then "$RUN_BIN" "$@"; else chdf "$@"; fi; }

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

# Seven resources in one dependency chain, so a single kill lands after
# every record-held instance has returned and inside the one marker window:
#
#   terraform_data.effect  record-backed. It has no cloud home, so its
#                          record is the only trace that it ran. Since
#                          #1944 that record is written the moment its
#                          apply step returns (internal/backend/local/
#                          hook_live_record.go), not after the whole walk.
#                          Its provisioner appends a line to effects.log,
#                          so "it ran" is counted rather than assumed.
#   random_password.db     record-backed: the record IS the password. Lost,
#                          the next plan regenerates it.
#   aws_iam_group_policy.deploy
#                          record-carried: its name is left for the provider
#                          to assign and the type has no tags, so the record
#                          is the only place its identity (group, name) is
#                          held. Lost, the next plan creates a second inline
#                          policy and the first stays on the group, owned by
#                          nothing. (aws_iam_access_key is the issue's own
#                          example, but the pinned emulator answers its
#                          GetAccessKeyLastUsed read empty, so no plan can
#                          bind one there.)
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
    random = {
      source  = "hashicorp/random"
      version = "= 3.7.2"
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

resource "random_password" "db" {
  length     = 24
  depends_on = [terraform_data.effect]
}

resource "aws_vpc" "main" {
  cidr_block = "$CIDR"
  depends_on = [random_password.db]
}

resource "aws_iam_group" "deploy" {
  name       = "$GROUP_NAME"
  depends_on = [aws_vpc.main]
}

resource "aws_iam_group_policy" "deploy" {
  group = aws_iam_group.deploy.name
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = ["s3:ListBucket"], Resource = "*" }]
  })
}

resource "aws_route53_zone" "dns" {
  name       = "$ZONE"
  depends_on = [aws_iam_group_policy.deploy]
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
# group_policies counts the IAM group's inline policies, read off the
# account.
group_policies() {
  local out
  out="$(awsl iam list-group-policies --group-name "$GROUP_NAME" --query 'length(PolicyNames)' --output text 2>/dev/null)" || out=""
  printf '%s' "${out:-0}"
}
# password_in_record prints random_password.db's result as its record holds
# it, or nothing when there is no record.
password_in_record() {
  local f
  f="$(find "$RECORDS" -type f -name "$(record_key random_password.db)" 2>/dev/null | head -1)"
  [ -n "$f" ] || return 0
  python3 - "$f" <<'PYEOF'
import json, sys
def find(o):
    if isinstance(o, dict):
        if isinstance(o.get("result"), str):
            return o["result"]
        for v in o.values():
            r = find(v)
            if r: return r
    elif isinstance(o, list):
        for v in o:
            r = find(v)
            if r: return r
    elif isinstance(o, str) and o[:1] in "{[":
        try: return find(json.loads(o))
        except ValueError: return None
    return None
print(find(json.load(open(sys.argv[1]))) or "")
PYEOF
}
record_paths() {
  if [ -d "$RECORDS" ]; then ( cd "$SMOKE_WORK" && find .tofu-records -type f | sort | tr '\n' ' ' ); else printf 'nothing'; fi
}

step "the claim"
explain \
  "A crash is easy to fake with the AWS CLI: a resource created and" \
  "tagged by hand, standing in for one a dead apply left behind. That" \
  "kills no real apply. This does, with" \
  "SIGKILL, mid-walk, at a point pinned by a count read off the account." \
  "" \
  "Four kinds of thing are created before the kill and each answers a" \
  "different question. A VPC, whose markers ride its create call, so" \
  "there is no window at all. A hosted zone, whose type reads" \
  "tag_on_create false in live/registry.json - the create call cannot" \
  "carry tags, so this fork writes them once the provider's create step" \
  "returns (#1084, #1512). An inline group policy whose name the provider" \
  "assigns and which has nowhere to carry a marker. And a terraform_data" \
  "and a random_password, whose" \
  "record is the object. The last two kinds live in the record store, and" \
  "since #1944 a record is written the moment its instance's apply step" \
  "returns. The claim is about what the next plan can name, and the" \
  "answer is not the same for the four."

step "1. init, and start the apply that is going to be killed"
cmd "choudoufu init && choudoufu apply -auto-approve   # to be killed"
logged a-killed-apply-hides-nothing-init "killed" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
[ "$(zones_named)" = "0" ] || fail "killed" "the account already holds a zone named $ZONE before anything ran"
( cd "$SMOKE_WORK" && run_chdf apply -auto-approve -input=false -no-color > "$SMOKE_WORK/apply-1.log" 2>&1 ) &
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
  "Reads none of which go through choudoufu. The VPC and whether it" \
  "carries its markers. The zone and the same question. The IAM group's" \
  "inline policies. effects.log, which the terraform_data's provisioner" \
  "appends a line to. And what is on disk: the record store, which holds" \
  "a record for every record-held instance that returned before the kill," \
  "and the state cache, which is written after the walk and so is not" \
  "there at all."
cmd "aws ec2 describe-vpcs ; aws route53 list-tags-for-resource ; ls .tofu-records ; ls .terraform"
echo "vpcs with cidr $CIDR: $(vpcs_named)   marked as aws_vpc.main: ${KILLED_VPC:-none}" | evidence
echo "hosted zones named $ZONE: $(zones_named) (id ${KILLED_ZONE:-none})   its markers: ${ZONE_MARKERS_AT_KILL:-none}" | evidence
echo "the estate's tagged ARNs, as the sweep reads them: $(swept_arns | tr '\n' ' ')" | evidence
EFFECT_RUNS="$(lines_in "$EFFECTS")"
echo "effects.log lines (times the record-backed effect has run): $EFFECT_RUNS" | evidence
POLICIES_AT_KILL="$(group_policies)"
PASSWORD_AT_KILL="$(password_in_record)"
echo "inline policies on $GROUP_NAME: $POLICIES_AT_KILL   random_password.db in its record: $([ -n "$PASSWORD_AT_KILL" ] && echo "${#PASSWORD_AT_KILL} characters" || echo none)" | evidence
echo "what the record store holds: $(record_paths)" | evidence
echo "state cache written: $([ -f "$CACHE" ] && echo yes || echo no)" | evidence
[ -n "$KILLED_VPC" ] || fail "killed" "the VPC the killed apply created carries no marker; internal/live/stamp puts them in the create request, so it is marked the instant it exists or this claim's first leg is gone"
[ "$EFFECT_RUNS" = "1" ] || fail "killed" "the record-backed effect ran $EFFECT_RUNS times, not once, so the kill did not land where this claim says it did"
[ "$POLICIES_AT_KILL" = "1" ] || fail "killed" "the IAM group holds $POLICIES_AT_KILL inline policies at the kill, not 1, so the kill did not land where this claim says it did"
[ ! -f "$CACHE" ] || fail "killed" "the killed run wrote a state cache, which is written after the walk"

if [ "${BREAK_EARLY_RECORD:-0}" = "1" ]; then
  step "BREAK_EARLY_RECORD control - no record until the walk is over"
  explain \
    "This binary writes no record until the whole walk is over, which is" \
    "how every apply behaved before #1944. The kill landed after the inline" \
    "policy, the password and the effect had all returned, so the record store" \
    "must hold none of them, and the re-run must duplicate what it cannot" \
    "see: a second inline policy on the group while the first stays, a" \
    "regenerated password, the effect run again. If it duplicates nothing," \
    "the main arm's 'no duplicate' proves nothing about the early record."
  for a in terraform_data.effect random_password.db aws_iam_group_policy.deploy; do
    [ "$(records_for "$a")" = "0" ] || fail "killed" "BREAK_EARLY_RECORD: the broken binary wrote a record for $a before the walk ended, so this control broke nothing"
  done
  BPLAN="$(cd "$SMOKE_WORK" && run_chdf plan -input=false -no-color 2>&1)" || fail "killed" "BREAK_EARLY_RECORD: the plan after the kill failed: $BPLAN"
  { grep -E 'will be created|^Plan:' <<< "$BPLAN" || true; } | evidence
  grep -q 'random_password.db will be created' <<< "$BPLAN" \
    || fail "killed" "BREAK_EARLY_RECORD: with no record the plan still did not propose generating random_password.db again, so the main arm's 'same password' cannot tell a record from none: $BPLAN"
  BAPPLY="$(cd "$SMOKE_WORK" && run_chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "killed" "BREAK_EARLY_RECORD: the re-run failed: $BAPPLY"
  BPOLICIES="$(group_policies)"
  BRUNS="$(lines_in "$EFFECTS")"
  echo "inline policies on $GROUP_NAME after the re-run: $BPOLICIES   effects.log lines: $BRUNS" | evidence
  if [ "$BPOLICIES" = "1" ] && [ "$BRUNS" = "1" ]; then
    fail "killed" "BREAK_EARLY_RECORD: with no early record the re-run still made no second inline policy and ran the effect once; the main arm's assertions cannot tell an early record from none"
  fi
  [ "$BPOLICIES" = "2" ] || fail "killed" "BREAK_EARLY_RECORD: the group holds $BPOLICIES inline policies after the re-run; the duplicate this control expects is exactly one"
  proof "caught - with the record written only at the end of the walk, the killed run left none, and the re-run created a second inline policy ($BPOLICIES on the group, the first still there and owned by nothing) and ran the effect $BRUNS times. That is #1944's defect, and the main arm's single policy and single run are what the early record buys."
  for k in $(awsl iam list-group-policies --group-name "$GROUP_NAME" --query 'PolicyNames[]' --output text 2>/dev/null); do
    awsl iam delete-group-policy --group-name "$GROUP_NAME" --policy-name "$k" >/dev/null 2>&1 || true
  done
  ( cd "$SMOKE_WORK" && run_chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

for a in terraform_data.effect random_password.db aws_iam_group_policy.deploy; do
  [ "$(records_for "$a")" = "1" ] \
    || fail "killed" "the killed run left no record for $a, whose apply step returned before the kill; since #1944 its record is written the moment it returns (internal/backend/local/hook_live_record.go)"
done
[ -n "$PASSWORD_AT_KILL" ] || fail "killed" "random_password.db's record holds no result to compare the re-run against"
if [ -n "$ZONE_MARKERS_AT_KILL" ]; then
  proof "the VPC is marked and so is the zone: the kill landed after the tag write that follows a tag_on_create=false create, and everything in the account belongs to the estate. The inline policy, the password and the effect each have a record: written when each returned, before the kill."
else
  proof "the VPC is marked, and the zone is NOT: the kill landed inside the window between CreateHostedZone and the marker write that follows the provider's create step. The sweep lists the VPC and cannot see the zone. The inline policy, the password and the effect each have a record: written when each returned, before the kill."
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
  "inline policy is bound from its record, and the password and the effect" \
  "are read back from theirs, so none of the three is proposed. What the" \
  "plan says about the zone is the measurement this claim exists for, and" \
  "it follows from whether the kill beat the marker."
cmd "choudoufu plan"
PLAN="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "killed" "the plan after the kill failed: $PLAN"
{ grep -E 'will be created|^Plan:' <<< "$PLAN" || true; } | evidence
grep -q 'Plan: [0-9]* to add, 0 to change, 0 to destroy' <<< "$PLAN" \
  || fail "killed" "the plan after the kill proposes a change or a destroy; nothing the dead apply left may be rebuilt or removed: $PLAN"
if grep -q 'aws_vpc.main will be created' <<< "$PLAN"; then
  fail "killed" "the plan proposes creating a VPC the account already holds under this estate's markers - the re-run would duplicate it: $PLAN"
fi
for a in terraform_data.effect random_password.db aws_iam_group_policy.deploy; do
  if grep -qE "$a will be (created|replaced)|$a must be replaced" <<< "$PLAN"; then
    fail "killed" "the plan proposes building $a again although its record was written before the kill: $PLAN"
  fi
done
proof "the marked VPC is not in the plan, because there is nothing to do to it: the dead apply's markers are the whole recovery. Nor are the inline policy, the password or the effect: each was recorded when its apply step returned, and the plan binds them from those records."
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
  "window on ten types. The inline policy is not duplicated, the password" \
  "is not regenerated and the effect does not run again, because each was" \
  "recorded when it returned."
cmd "choudoufu apply -auto-approve"
A2_RC=0
A2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || A2_RC=$?
{ grep -E 'Apply complete|Resources: [0-9]+ added|Error:' <<< "$A2" | head -3 || true; } | evidence
[ "$A2_RC" = "0" ] || fail "killed" "the re-run failed: $A2"
VPCS_AFTER="$(vpcs_named)"
ZONES_AFTER="$(zones_named)"
EFFECT_RUNS_AFTER="$(lines_in "$EFFECTS")"
POLICIES_AFTER="$(group_policies)"
PASSWORD_AFTER="$(password_in_record)"
echo "vpcs with cidr $CIDR: $VPCS_AFTER   hosted zones named $ZONE: $ZONES_AFTER   effects.log lines: $EFFECT_RUNS_AFTER   inline policies: $POLICIES_AFTER" | evidence
echo "random_password.db unchanged since the kill: $([ "$PASSWORD_AFTER" = "$PASSWORD_AT_KILL" ] && echo yes || echo no)" | evidence
echo "what the record store holds now: $(record_paths)" | evidence
[ "$VPCS_AFTER" = "1" ] \
  || fail "killed" "the account holds $VPCS_AFTER VPCs with this estate's cidr; the re-run duplicated the marked one the killed apply created"
[ "$EFFECT_RUNS_AFTER" = "1" ] \
  || fail "killed" "the effect ran $EFFECT_RUNS_AFTER times in total; it was recorded before the kill, so the re-run must not run it again"
[ "$POLICIES_AFTER" = "1" ] \
  || fail "killed" "the IAM group holds $POLICIES_AFTER inline policies after the re-run; the policy the killed apply created was recorded, so the re-run must bind it, not create a second"
[ "$PASSWORD_AFTER" = "$PASSWORD_AT_KILL" ] \
  || fail "killed" "random_password.db's value changed across the re-run; its record was written before the kill, so the re-run must keep it"
if [ -n "$ZONE_MARKERS_AT_KILL" ]; then
  [ "$ZONES_AFTER" = "1" ] || fail "killed" "the zone was marked before the kill and the re-run still left $ZONES_AFTER of them"
  proof "one VPC, one zone, one inline policy, the same password, and the effect run once: everything the dead apply marked or recorded was bound, not rebuilt."
else
  [ "$ZONES_AFTER" = "2" ] \
    || fail "killed" "the zone the killed apply left is unmarked and the account holds $ZONES_AFTER of them after the re-run; the residue this claim reports is exactly one orphan and one owned zone"
  proof "one VPC, because it was marked. One inline policy, the same password and the effect run once, because they were recorded. TWO zones, because one of them was neither: the re-run owns the zone it just made and the account still holds the unmarked one. That is the whole cost of the window, and the next step pays it."
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

step "7. now lose every local file"
explain \
  "The other disaster is the lost laptop. Everything stock would call the" \
  "state - the cache this re-run wrote, the lock file, the whole" \
  ".terraform directory - is deleted. Init afterwards re-downloads tools;" \
  "it recovers no knowledge, because the knowledge was never local: the" \
  "markers are on the resources and the records are in the record store," \
  "which for a team is a bucket and here is the implied local one."
cmd "rm -rf .terraform .terraform.lock.hcl terraform.tfstate* ; choudoufu init ; choudoufu plan"
[ -f "$CACHE" ] || fail "killed" "expected the re-run to have written the state cache before the wipe"
rm -rf "$SMOKE_WORK"/.terraform "$SMOKE_WORK"/.terraform.lock.hcl "$SMOKE_WORK"/terraform.tfstate*
echo "everything left on disk: $(ls -A "$SMOKE_WORK" | tr '\n' ' ')" | evidence
logged a-killed-apply-hides-nothing-reinit "killed" "re-init after the wipe failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
P3="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "killed" "the plan after the wipe failed: $P3"
grep -E 'No changes.' <<< "$P3" | head -1 | evidence
grep -q 'No changes.' <<< "$P3" || fail "killed" "losing the local files changed the answer: $P3"
proof "a plan from the .tf files, the records and the cloud alone: nothing that was deleted was a record of anything."

step "8. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "killed" "teardown failed: $DOUT"
destroyed_exactly "killed" 7 "$DOUT"
[ "$(group_policies)" = "0" ] || fail "killed" "the destroy left $(group_policies) inline policies on the group"
proof "seven destroyed - the VPC and the inline policy the killed apply created were full citizens of the estate from the moment they were bound."

echo "  What you watched: a real apply killed with SIGKILL at a point pinned"
echo "  by the account's own resource count, and the three things it left"
echo "  behind measured apart. The VPC was marked in its create call, so the"
echo "  next plan asked nothing of it and the re-run bound it. The inline"
echo "  policy, the password and the effect were each recorded when they"
echo "  returned, so the re-run bound the policy, kept the password and did not"
echo "  run the effect again. And the hosted zone - one of ten"
echo "  types whose create call cannot carry a tag - was killed before its"
echo "  marker landed, so nothing could claim it and the re-run built a"
echo "  second one. That window is the one thing here that a re-run does not"
echo "  fix, it is bounded to those ten types, and it is measured above"
echo "  rather than left for a reader to find. Then every local file went,"
echo "  and the next plan came back clean from the markers and the records."
