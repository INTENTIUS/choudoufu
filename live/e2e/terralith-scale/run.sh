#!/usr/bin/env bash
set -uo pipefail

# live/e2e/terralith-scale/run.sh - the crossing script for the one estate
# shaped like the thing this product is FOR: a single-state monolith a
# stranger would bring to an adoption (#546), rather than a module example.
# tools/terralith-gen builds it (issue #564, extended by #574's count/
# for_each/module-nested expansion); at -scale 1 that is 79 resources across
# IAM, ECS, Route 53 and EC2, of which 38 are taggable and 41 are untaggable
# and compose their identity from an already-stamped parent
# (live/e2e/terralith-scale/MIGRATION.md's own ratification table).
#
# WHAT CHANGED, AND WHAT DID NOT (issue #643's board repair)
#
# This script used to be deliberately choudoufu-free: #564's own proof that
# the GENERATOR emits valid, appliable, destroyable STOCK Terraform, with
# every stage past cold_deploy reported not_run because the choudoufu binary
# was not invoked anywhere. That proof is still here, intact and unweakened,
# inside cold_deploy (part A2 below): stock applies the unmodified generator
# output, the account is enumerated (never counted) across every resource
# kind the estate uses, a deliberately-added role proves that enumeration
# has teeth, a plain stock destroy removes all 79, and the account is
# enumerated empty afterwards. What is new is everything after it: the same
# generated estate is now crossed against choudoufu, stage by stage, with
# stock as the oracle throughout.
#
# It is not live/live-cert/terralith-scale.sh with floci swapped in either.
# That script certifies four stages against a real, throttling AWS account
# and instruments for pagination and backoff; this one runs the full active
# stage set against the pinned emulator and carries the stock-side oracle
# and the BREAK control every stage in live/GAUNTLET.md names. The two
# overlap on cold_deploy/migrate/test_plan/test_apply and are deliberately
# kept apart (tools/gauntlet/livecert.go's own doc comment): an emulator row
# and a live-AWS certification are different evidence about different
# targets.
#
# TWO ACCOUNTS, AND WHY
#
#   COLD  ($FLOCI_PORT)      stock terraform applies the estate here and its
#                            terraform.tfstate is kept. That state file is
#                            what migrate adopts from, and this account's
#                            cloud is the oracle greenfield is compared to.
#                            Everything choudoufu does after migration -
#                            drift, rename, remove, count, replace - happens
#                            here, on infrastructure stock built.
#   GREEN ($FLOCI_PORT + 1)  #564's own stock apply/destroy proof runs here
#                            FIRST, which leaves this account enumerated
#                            empty - the precondition greenfield's own
#                            Proves text asks for ("from an empty account").
#                            choudoufu then applies the same configuration
#                            into it directly, no migration. Once the
#                            greenfield oracle has read both clouds, this
#                            idle account is where day2_count's stock oracle
#                            applies its own separately-prefixed fixture.
#
# Both containers run the same pinned image and neither is ever a source of
# truth about the other: every assertion below reads the AWS CLI or a
# command's real output, never choudoufu's own report about itself, and
# never an exit code standing in for a verdict.
#
# THE STOCK ORACLES RUN BEFORE MIGRATION, ON PURPOSE
#
# day2_rename's, day2_remove's and day2_replace's stock oracles are computed
# in part B, against copies of cold_deploy's own state, before live-import
# ever writes a marker. Running them afterwards would confound every one of
# them: stock's configuration has no argument to write tofu-estate and
# tofu-address into, so a stock plan over a migrated estate proposes
# stripping the markers off all 38 marked objects, and "Plan: 0 to add, 0 to
# change, 2 to destroy" could never be asserted. That is the same reason
# live/e2e/reference-ec2-vpc/run.sh computes its own oracles at B1.5-B1.8.
#
#   bash live/e2e/terralith-scale/run.sh
#
# Needs Docker, the AWS CLI, jq and a real stock `terraform` on PATH.
#
# Env overrides:
#   SCALE        the -scale value passed to terralith-gen (default 1, the
#                "genuinely small tier" #564 asks to prove first, and the
#                tier #546's own rule says to hold until it is proven).
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the COLD emulator (default 4745); GREEN is
#                FLOCI_PORT+1, within the stride tools/gauntlet's own port
#                allocator reserves (run.go, parallelPortStride).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# The BREAK controls. Each one forces the exact defect its stage exists to
# catch (live/GAUNTLET.md's own Break line, verbatim where it fits) and the
# assertion under it must then fail. A BREAK run ends the script once its
# control has been proved, because every later stage starts from the state
# the broken one would have left:
#
#   BREAK_GREENFIELD  drop one resource kind from the greenfield side's
#                     expected inventory; the object-by-object comparison
#                     must fail.
#   BREAK_MIGRATE     expect one fewer instance on live-import's summary
#                     line; the assertion must fail.
#   BREAK_PLAN        corrupt one expected identity string; test_plan must
#                     fail on that string and nothing else.
#   BREAK_APPLY       expect a different post-apply inventory; the
#                     unchanged-inventory assertion must fail.
#   BREAK_DRIFT       mutate a SECOND live object out of band; the
#                     single-object assertion must fail.
#   BREAK_RENAME      rename without the moved block; the plan must show a
#                     destroy and a create.
#   BREAK_REMOVE      keep the blocks; no destroy may be proposed.
#   BREAK_COUNT       expect a different instance to be destroyed; the
#                     assertion must fail.
#   BREAK_REPLACE     skip the destroy half (re-create the old object
#                     carrying the same marker, through the AWS CLI); the
#                     next plan must report a collision rather than
#                     proposing nothing.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Read the
# `GAUNTLET stage=` verdict lines, never the exit code.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=live/e2e/lib/gauntlet.sh
source "$ROOT/live/e2e/lib/gauntlet.sh"

WORK="$(mktemp -d)"
SCALE="${SCALE:-1}"
FLOCI_PORT="${FLOCI_PORT:-4745}"
GREEN_PORT=$((FLOCI_PORT + 1))
FLOCI_NAME="choudoufu-terralith-scale-$$"
GREEN_NAME="choudoufu-terralith-scale-green-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"
GREEN_ENDPOINT="http://127.0.0.1:${GREEN_PORT}"
REGION="us-east-1"
PREFIX="tls$$"
ESTATE="${PREFIX}-terralith"

GEN="$WORK/gen"              # pristine generator output, never applied
COLD="$WORK/cold"            # stock's own directory, against COLD
STOCKGREEN="$WORK/stockgreen" # stock's apply/destroy proof, against GREEN
GREENDIR="$WORK/greenfield"  # choudoufu greenfield, against GREEN
ADOPTED="$WORK/adopted"      # choudoufu post-migration, against COLD

# Composition formulas track tools/terralith-gen and MUST move with it.
# EXPECTED is the same 74*SCALE+5 this script has always asserted;
# TAGGABLE (33*SCALE+5) is the marker-eligible half, the number
# live/live-cert/terralith-scale.sh asserts against real AWS.
EXPECTED=$((74 * SCALE + 5))
TAGGABLE=$((33 * SCALE + 5))
UNTAGGABLE=$((EXPECTED - TAGGABLE))

cleanup() {
  docker rm -f "$FLOCI_NAME" "$GREEN_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
CURRENT_STAGE=""
fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "${CURRENT_STAGE:-}" ]; then gauntlet_stage "$CURRENT_STAGE" fail "$*"; fi
  exit 1
}

# not_run_rest reports every stage named, in order, as not_run with one
# shared reason. Used by the BREAK legs (which deliberately stop early) and
# by the "did not get this far" path, so a row always says WHY it is empty
# rather than merely that it is.
not_run_rest() {
  local reason="$1"; shift
  local s
  for s in "$@"; do gauntlet_stage "$s" not_run "$reason"; done
}

x() { local ep="$1"; shift; aws --endpoint-url "$ep" --region "$REGION" "$@"; }
awsl() { x "$ENDPOINT" "$@"; }
awsg() { x "$GREEN_ENDPOINT" "$@"; }

sed_i() { local f="$1"; shift; local t; t="$(mktemp)"; sed "$@" "$f" > "$t" && mv "$t" "$f"; }

# plan_is_noop is true when a plan output proposes no resource action at
# all. Both forms count, and which one a plan prints is not something the
# script controls: "No changes." is what an ordinary converged plan says,
# while a plan that carries `moved` blocks prints the moves it recorded and
# then "Plan: 0 to add, 0 to change, 0 to destroy." instead. Demanding the
# first string alone made this script's own day2_rename stock oracle fail on
# its first run against a plan that was, in fact, exactly the zero churn the
# oracle exists to establish - a stale assertion, not a defect (HANDOFF's
# "when an assertion breaks right after a fix lands").
plan_is_noop() {
  grep -qF 'No changes. Your infrastructure matches the configuration.' <<< "$1" && return 0
  grep -qF 'Plan: 0 to add, 0 to change, 0 to destroy.' <<< "$1" && return 0
  return 1
}

# addr_type_counts turns a list of resource addresses on stdin into sorted
# "<type> <count>" lines, stripping module-instance prefixes so
# module.team_pod["pod-a"].aws_iam_role.pod_role[0] counts as one
# aws_iam_role. record_type_counts produces the same shape from a record
# store's own per-type directories, so the two can be compared directly.
addr_type_counts() {
  sed -E 's/^(module\.[^.]+\.)+//' | sed -E 's/\..*$//' | grep -v '^$' \
    | sort | uniq -c | awk '{printf "%s %s\n", $2, $1}' | sort
}
record_type_counts() {
  local base="$1" d
  for d in "$base"/*; do
    [ -d "$d" ] || continue
    printf '%s %s\n' "$(basename "$d")" \
      "$(find "$d" -type f ! -name '*.lock' ! -name '*.tmp-*' | wc -l | tr -d ' ')"
  done | sort
}

wait_healthy() {
  local ep="$1" h
  for _ in $(seq 1 45); do
    h="$(curl -fs "${ep}/_localstack/health" 2>/dev/null)" || true
    case "${h:-}" in *'"ec2"'*) return 0 ;; esac
    sleep 2
  done
  return 1
}

# ── the enumerated inventory (never a bare count) ────────────────────────
#
# inventory prints one line per live object this run's own prefix could have
# created, across every resource kind the estate uses. cold_deploy's
# empty-account assertion, and test_apply's unchanged-estate assertion, both
# read this rather than a number: this repository has shipped a bare count
# that measured nothing before (CLAUDE.md), and an enumeration that comes
# back empty because the CLI errored is caught by the non-empty guard each
# caller applies before trusting it.
inventory() {
  local ep="$1"
  {
    x "$ep" iam list-roles --query "Roles[?starts_with(RoleName, '${PREFIX}-')].RoleName" --output text
    x "$ep" iam list-policies --scope Local --query "Policies[?starts_with(PolicyName, '${PREFIX}-')].PolicyName" --output text
    x "$ep" iam list-instance-profiles --query "InstanceProfiles[?starts_with(InstanceProfileName, '${PREFIX}-')].InstanceProfileName" --output text
    x "$ep" route53 list-hosted-zones --query "HostedZones[?Name=='${PREFIX}.terralith.test.'].Id" --output text
    x "$ep" ecs list-clusters --query 'clusterArns' --output text | tr '\t' '\n' | grep -F "${PREFIX}-cluster" || true
    x "$ep" ec2 describe-vpcs --filters "Name=tag:Name,Values=${PREFIX}-vpc" --query 'Vpcs[].VpcId' --output text
  } | tr '\t' '\n' | grep -v '^$' | sort
}

# ── the structural shape, for greenfield's object-by-object oracle ───────
#
# shape reads structural facts straight off the AWS CLI for one endpoint -
# role names and each role's inline policies and attachments, customer-
# managed policy names, instance-profile names and the role each holds, the
# VPC/subnet/security-group shape, the ECS cluster/task-definition/service
# shape, the hosted zone and every record in it. Never through tofu state on
# either side, so the comparison cannot be fooled by choudoufu's own
# bookkeeping agreeing with itself, and NEVER a tag: the plan-fidelity
# contract normalises the marker tags out of both sides, and the way to
# normalise them out of a cloud comparison is to never read them here at
# all. Live ids (vpc-..., sg-..., the zone id, an ARN's random suffix) are
# also never printed: two separate accounts mint different ones for the same
# declaration, so comparing them would be comparing the emulator's id
# allocator, not the estate.
shape() {
  local ep="$1" r p pr zid
  {
    for r in $(x "$ep" iam list-roles --query "Roles[?starts_with(RoleName, '${PREFIX}-')].RoleName" --output text | tr '\t' '\n' | sort); do
      printf 'role name=%s\n' "$r"
      for p in $(x "$ep" iam list-role-policies --role-name "$r" --query 'PolicyNames' --output text 2>/dev/null | tr '\t' '\n' | sort); do
        printf 'role-inline role=%s name=%s\n' "$r" "$p"
      done
      for p in $(x "$ep" iam list-attached-role-policies --role-name "$r" --query 'AttachedPolicies[].PolicyArn' --output text 2>/dev/null | tr '\t' '\n' | sort); do
        printf 'role-attach role=%s arn=%s\n' "$r" "$p"
      done
    done
    x "$ep" iam list-policies --scope Local --query "Policies[?starts_with(PolicyName, '${PREFIX}-')].PolicyName" --output text \
      | tr '\t' '\n' | grep -v '^$' | sed 's/^/policy name=/'
    for pr in $(x "$ep" iam list-instance-profiles --query "InstanceProfiles[?starts_with(InstanceProfileName, '${PREFIX}-')].InstanceProfileName" --output text | tr '\t' '\n' | sort); do
      printf 'profile name=%s role=%s\n' "$pr" \
        "$(x "$ep" iam get-instance-profile --instance-profile-name "$pr" --query 'InstanceProfile.Roles[0].RoleName' --output text 2>/dev/null)"
    done
    x "$ep" ec2 describe-vpcs --filters "Name=tag:Name,Values=${PREFIX}-vpc" --query 'Vpcs[0].CidrBlock' --output text | sed 's/^/vpc cidr=/'
    x "$ep" ec2 describe-subnets --filters "Name=tag:Name,Values=${PREFIX}-subnet" --query 'Subnets[0].[CidrBlock,AvailabilityZone]' --output text \
      | awk 'NF{print "subnet cidr="$1" az="$2}'
    x "$ep" ec2 describe-security-groups --filters "Name=group-name,Values=${PREFIX}-ecs-sg" --query 'SecurityGroups[0].IpPermissionsEgress[].[IpProtocol,FromPort,ToPort]' --output text \
      | awk 'NF{print "sg-egress proto="$1" from="$2" to="$3}' | sort
    local carn
    carn="$(x "$ep" ecs list-clusters --query 'clusterArns' --output text | tr '\t' '\n' | grep -F "/${PREFIX}-cluster" || true)"
    if [ -n "$carn" ]; then
      printf 'cluster name=%s\n' "${carn##*/}"
      x "$ep" ecs list-services --cluster "$carn" --query 'serviceArns' --output text 2>/dev/null | tr '\t' '\n' | grep -v '^$' | sort | while read -r sarn; do
        x "$ep" ecs describe-services --cluster "$carn" --services "$sarn" \
          --query 'services[0].[serviceName,desiredCount,launchType]' --output text 2>/dev/null \
          | awk 'NF{print "service name="$1" desired="$2" launch="$3}'
      done
    fi
    x "$ep" ecs list-task-definitions --family-prefix "${PREFIX}-svc-" --status ACTIVE --query 'taskDefinitionArns' --output text 2>/dev/null \
      | tr '\t' '\n' | grep -v '^$' | sed 's#.*/#taskdef family-revision=#' | sort
    printf 'zone name=%s.terralith.test.\n' "$PREFIX"
    zid="$(x "$ep" route53 list-hosted-zones --query "HostedZones[?Name=='${PREFIX}.terralith.test.'].Id" --output text)"
    if [ -n "$zid" ] && [ "$zid" != "None" ]; then
      x "$ep" route53 list-resource-record-sets --hosted-zone-id "$zid" \
        --query "ResourceRecordSets[?Type!='NS' && Type!='SOA'].[Name,Type,TTL,ResourceRecords[0].Value]" --output text 2>/dev/null \
        | awk 'NF{print "record name="$1" type="$2" ttl="$3" value="$4}' | sort
    fi
  } | sort
}

# ── configuration rendering ──────────────────────────────────────────────
#
# render_config copies the pristine generator output over a directory's .tf
# files (leaving .terraform, terraform.tfstate and .tofu-records alone, so a
# directory is initialised once) and then applies a named list of edits. The
# day-2 stages are cumulative - a rename stays renamed while the next stage
# removes a block - so each call names the FULL list rather than editing
# whatever the previous stage happened to leave behind. Every edit touches
# iam.tf only, and only blocks nothing else in the estate references:
# `grep -n 'aws_iam_instance_profile\.\|aws_iam_role_policy\.' *.tf
# modules/team_pod/*.tf` over the generated estate returns nothing, so an
# instance profile or an inline policy can be renamed, removed or replaced
# without a single other configuration edit.
inject_live() {
  local dir="$1" t
  t="$(mktemp)"
  awk -v estate="$ESTATE" '
    !inserted && /^}$/ {
      print ""
      print "  live {"
      print "    estate = \"" estate "\""
      print "    record_store \"local\" {"
      print "      path = \".tofu-records\""
      print "    }"
      print "  }"
      inserted = 1
    }
    { print }
  ' "$dir/versions.tf" > "$t" && mv "$t" "$dir/versions.tf"
  grep -qF '  live {' "$dir/versions.tf" || fail "inject_live did not add a live block to $dir/versions.tf"
}

drop_block() {
  local f="$1" type="$2" name="$3" t
  t="$(mktemp)"
  awk -v hdr="resource \"$type\" \"$name\" {" '
    $0 == hdr { drop = 1; next }
    drop && $0 == "}" { drop = 0; next }
    !drop { print }
  ' "$f" > "$t" && mv "$t" "$f"
  grep -qF "resource \"$type\" \"$name\" {" "$f" \
    && fail "drop_block did not remove $type.$name from $f"
  return 0
}

replace_profile_4() {
  local f="$1" t
  t="$(mktemp)"
  awk -v hdr='resource "aws_iam_instance_profile" "team_0004_profile" {' \
      -v newname="${PREFIX}-team-0004-profile-replaced" '
    $0 == hdr { inb = 1; print; next }
    inb && $0 == "}" {
      print ""
      print "  lifecycle {"
      print "    create_before_destroy = true"
      print "  }"
      print "}"
      inb = 0
      next
    }
    inb && $1 == "name" { printf "  name = \"%s\"\n", newname; next }
    { print }
  ' "$f" > "$t" && mv "$t" "$f"
  grep -qF "${PREFIX}-team-0004-profile-replaced" "$f" \
    || fail "replace_profile_4 did not rewrite team_0004_profile's name in $f"
  grep -qF 'create_before_destroy = true' "$f" \
    || fail "replace_profile_4 did not add create_before_destroy in $f"
}

render_config() {
  local dir="$1"; shift
  mkdir -p "$dir/modules/team_pod"
  cp "$GEN"/*.tf "$dir"/ || fail "could not copy the generated .tf files into $dir"
  cp "$GEN"/modules/team_pod/*.tf "$dir/modules/team_pod"/ || fail "could not copy the generated module into $dir"
  local e
  for e in "$@"; do
    case "$e" in
      live) inject_live "$dir" ;;
      rename0)
        sed_i "$dir/iam.tf" 's/^resource "aws_iam_instance_profile" "team_0000_profile" {$/resource "aws_iam_instance_profile" "team_0000_profile_renamed" {/'
        grep -qF 'resource "aws_iam_instance_profile" "team_0000_profile_renamed" {' "$dir/iam.tf" \
          || fail "rename0 edit did not apply to $dir/iam.tf"
        ;;
      moved0)
        printf '\nmoved {\n  from = aws_iam_instance_profile.team_0000_profile\n  to   = aws_iam_instance_profile.team_0000_profile_renamed\n}\n' >> "$dir/iam.tf"
        ;;
      rename1)
        sed_i "$dir/iam.tf" 's/^resource "aws_iam_instance_profile" "team_0001_profile" {$/resource "aws_iam_instance_profile" "team_0001_profile_renamed" {/'
        grep -qF 'resource "aws_iam_instance_profile" "team_0001_profile_renamed" {' "$dir/iam.tf" \
          || fail "rename1 edit did not apply to $dir/iam.tf"
        ;;
      moved1)
        printf '\nmoved {\n  from = aws_iam_instance_profile.team_0001_profile\n  to   = aws_iam_instance_profile.team_0001_profile_renamed\n}\n' >> "$dir/iam.tf"
        ;;
      remove2)
        drop_block "$dir/iam.tf" aws_iam_instance_profile team_0002_profile
        drop_block "$dir/iam.tf" aws_iam_role_policy team_0002_inline
        ;;
      count1)
        sed_i "$dir/iam.tf" -E 's/^([[:space:]]*count[[:space:]]*)= 2$/\1= 1/'
        [ "$(grep -cE '^[[:space:]]*count[[:space:]]*= 1$' "$dir/iam.tf")" = "6" ] \
          || fail "count1 edit did not rewrite all six count_team count arguments in $dir/iam.tf"
        ;;
      replace4) replace_profile_4 "$dir/iam.tf" ;;
      *) fail "render_config: unknown edit $e" ;;
    esac
  done
}

# ── identity assertions, read by value through the AWS CLI ───────────────
#
# The representative set live/GAUNTLET.md's test_plan stage asks for. It
# deliberately spans every address SHAPE this estate produces, because an
# empty plan alone is not a pass: a wrong identity can converge, and the
# shapes that go wrong are the indexed ones.
#
#   aws_route53_zone.main                                  root, scalar
#   aws_iam_role.team_0000_role                            root, scalar
#   aws_iam_role.count_team[1]                             root, count-indexed
#   module.team_pod["pod-a"].aws_iam_role.pod_role[0]      module-nested, keyed
#                                                          module + counted
#                                                          resource - the
#                                                          double-indexed shape
#                                                          marker_module_prefix
#                                                          exists to serve (#378)
#   aws_ecs_cluster.main                                   a non-EC2 tagging API
#   aws_vpc.main                                           the EC2 tagging API
#
# Each reader below goes to a DIFFERENT AWS tagging surface (IAM's per-type
# calls, Route 53's own, ECS's own, EC2's) on purpose: "the marker is
# written" is a different claim per service, and this estate is the only one
# on the board that can ask all four in one run.
marker_of_role()    { x "$1" iam list-role-tags --role-name "$2" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null; }
marker_of_profile() { x "$1" iam list-instance-profile-tags --instance-profile-name "$2" --query "Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null; }
marker_of_vpc() {
  local ep="$1" vid
  vid="$(x "$ep" ec2 describe-vpcs --filters "Name=tag:Name,Values=${PREFIX}-vpc" --query 'Vpcs[0].VpcId' --output text)"
  [ -n "$vid" ] && [ "$vid" != "None" ] || { printf 'NO-VPC\n'; return; }
  x "$ep" ec2 describe-tags --filters "Name=resource-id,Values=$vid" "Name=key,Values=tofu-address" --query 'Tags[0].Value' --output text 2>/dev/null
}
marker_of_zone() {
  local ep="$1" zid
  zid="$(x "$ep" route53 list-hosted-zones --query "HostedZones[?Name=='${PREFIX}.terralith.test.'].Id" --output text)"
  [ -n "$zid" ] && [ "$zid" != "None" ] || { printf 'NO-ZONE\n'; return; }
  x "$ep" route53 list-tags-for-resource --resource-type hostedzone --resource-id "${zid#/hostedzone/}" \
    --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null
}
marker_of_cluster() {
  local ep="$1" carn
  carn="$(x "$ep" ecs list-clusters --query 'clusterArns' --output text | tr '\t' '\n' | grep -F "/${PREFIX}-cluster" || true)"
  [ -n "$carn" ] || { printf 'NO-CLUSTER\n'; return; }
  x "$ep" ecs list-tags-for-resource --resource-arn "$carn" --query "tags[?key=='tofu-address'].value | [0]" --output text 2>/dev/null
}

# escape_address applies live/MARKERS.md's own escaping rule to an ordinary
# OpenTofu address, so every expectation below is written as the address a
# reader of the configuration would write and turned into a tag value by the
# published rule rather than by whatever the implementation happens to do.
# That direction matters: MARKERS.md states the comparison contract as
# "escape the known config address, compare strings, never decode the tag
# blind", and a check that read the tag and decoded it would be a check
# written from the implementation.
#
# The rule, verbatim from that document: escape the content of every
# instance key first, then replace every "[" with ":", delete every "]",
# delete every '"'. Step 1 is a no-op for everything this estate produces -
# a count index is only ever digits, and the one for_each key that reaches a
# marker here ("pod-a") is already inside the AWS-legal set - so only steps
# 2-4 are implemented, and an estate that grew a key needing step 1 would
# have to extend this.
escape_address() { printf '%s' "$1" | sed -e 's/\[/:/g' -e 's/\]//g' -e 's/"//g'; }

# check_identity prints one line if the live tofu-address on an object does
# not equal the escaped form of the address it should carry, and nothing at
# all when it matches. $1 label, $2 the value read from the AWS CLI, $3 the
# ordinary (unescaped) address it must carry.
check_identity() {
  local want
  want="$(escape_address "$3")"
  [ "$2" = "$want" ] \
    || printf '%s: tofu-address=%s want=%s (the escaped form of %s, per live/MARKERS.md)\n' "$1" "$2" "$want" "$3"
}

# identity_mismatches prints one line per representative identity whose live
# tofu-address does not equal what it should carry, and nothing at all when
# every one matches. $1 is the endpoint; $2, when non-empty, replaces the
# EXPECTED address of the count-indexed entry, which is how BREAK_PLAN
# corrupts exactly one expected identity string.
identity_mismatches() {
  local ep="$1" corrupt="${2:-}"
  local want_count="aws_iam_role.count_team[1]"
  [ -n "$corrupt" ] && want_count="$corrupt"
  check_identity "zone ${PREFIX}.terralith.test."     "$(marker_of_zone "$ep")"                                   'aws_route53_zone.main'
  check_identity "role ${PREFIX}-team-0000-role"      "$(marker_of_role "$ep" "${PREFIX}-team-0000-role")"         'aws_iam_role.team_0000_role'
  check_identity "role ${PREFIX}-count-team-0001-role" "$(marker_of_role "$ep" "${PREFIX}-count-team-0001-role")"  "$want_count"
  check_identity "role ${PREFIX}-pod-a-team-0000-role" "$(marker_of_role "$ep" "${PREFIX}-pod-a-team-0000-role")"  'module.team_pod["pod-a"].aws_iam_role.pod_role[0]'
  check_identity "cluster ${PREFIX}-cluster"          "$(marker_of_cluster "$ep")"                                'aws_ecs_cluster.main'
  check_identity "vpc ${PREFIX}-vpc"                  "$(marker_of_vpc "$ep")"                                    'aws_vpc.main'
}

# ── 0. tools ─────────────────────────────────────────────────────────────
gauntlet_begin
gauntlet_begin_stage cold_deploy
log "=== 0. tools (scale=$SCALE prefix=$PREFIX estate=$ESTATE) ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v jq >/dev/null 2>&1 || fail "jq is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "a real terraform binary is not on PATH - stock is the oracle for every stage in this script, so terraform itself is required, not a checked-out tool"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && env -u PWD go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU"
fi

export TF_PLUGIN_CACHE_DIR="$WORK/plugin-cache"
mkdir -p "$TF_PLUGIN_CACHE_DIR"

# ── 1. generate ──────────────────────────────────────────────────────────
log "=== 1. terralith-gen -scale $SCALE -prefix $PREFIX ==="
( cd "$ROOT" && env -u PWD go run ./tools/terralith-gen -scale "$SCALE" -prefix "$PREFIX" -out "$GEN" ) \
  || fail "terralith-gen failed"
log "  expect ${EXPECTED} resources at scale=${SCALE}, of which ${TAGGABLE} are taggable and ${UNTAGGABLE} untaggable"

# ── 2. two emulators ─────────────────────────────────────────────────────
log "=== 2. floci: COLD on :$FLOCI_PORT, GREEN on :$GREEN_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
docker run -d --rm -p "${GREEN_PORT}:4566" --name "$GREEN_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $GREEN_NAME failed"
wait_healthy "$ENDPOINT" || fail "the COLD floci did not come up healthy (ec2) at $ENDPOINT"
wait_healthy "$GREEN_ENDPOINT" || fail "the GREEN floci did not come up healthy (ec2) at $GREEN_ENDPOINT"
log "  both healthy"

export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# ══════════════════════════════════════════════════════════════════════════
# PART A: COLD DEPLOY
# ══════════════════════════════════════════════════════════════════════════

log "=== A1. cold_deploy: stock terraform applies the unmodified estate into COLD ==="
render_config "$COLD"
( cd "$COLD" && AWS_ENDPOINT_URL="$ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$COLD" && AWS_ENDPOINT_URL="$ENDPOINT" terraform init -input=false -no-color 2>&1 | tail -20 ); fail "stock terraform init failed in COLD"; }
COLD_APPLY="$(cd "$COLD" && AWS_ENDPOINT_URL="$ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$COLD_APPLY" | grep -E '^Error|^│' | head -30
  fail "stock terraform apply failed in COLD"; }
grep -qE "Apply complete! Resources: ${EXPECTED} added, 0 changed, 0 destroyed" <<< "$COLD_APPLY" \
  || { grep -E 'Apply complete' <<< "$COLD_APPLY"; fail "stock's COLD apply did not create exactly ${EXPECTED} resources"; }
[ -f "$COLD/terraform.tfstate" ] || fail "stock's COLD apply left no state file for migrate to adopt from"
log "  $(grep -E 'Apply complete' <<< "$COLD_APPLY")"

COLD_INV="$(inventory "$ENDPOINT")"
COLD_INV_N="$(grep -c . <<< "$COLD_INV" || true)"
[ "$COLD_INV_N" -gt 0 ] || fail "the COLD inventory reports 0 objects right after a successful apply - the check is not measuring anything"
log "  COLD inventory: $COLD_INV_N objects across IAM/Route53/ECS/EC2"

# The estate is genuinely unmarked at this point - migrate's whole premise.
COLD_UNMARKED="$(marker_of_role "$ENDPOINT" "${PREFIX}-team-0000-role")"
[ "$COLD_UNMARKED" = "None" ] || [ -z "$COLD_UNMARKED" ] \
  || fail "the stock-applied role ${PREFIX}-team-0000-role already carries tofu-address=$COLD_UNMARKED before migration - this run would prove nothing"
log "  confirmed unmarked: ${PREFIX}-team-0000-role carries no tofu-address tag"

# A2 is issue #564's own proof, unchanged in substance: stock applies the
# generator's output and a stock destroy takes it all back down, with the
# account enumerated (never counted) before and after and a deliberately-
# added object proving the enumeration has teeth. It runs in GREEN, which
# leaves that account enumerated empty - exactly the precondition
# greenfield's own Proves text needs ("from an empty account").
log "=== A2. cold_deploy: the same stock apply into GREEN, then a stock destroy back to an enumerated-empty account (#564's own proof) ==="
render_config "$STOCKGREEN"
( cd "$STOCKGREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) \
  || fail "stock terraform init failed in GREEN"
GREEN_APPLY="$(cd "$STOCKGREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_APPLY" | grep -E '^Error|^│' | head -30
  fail "stock terraform apply failed in GREEN"; }
grep -qE "Apply complete! Resources: ${EXPECTED} added, 0 changed, 0 destroyed" <<< "$GREEN_APPLY" \
  || { grep -E 'Apply complete' <<< "$GREEN_APPLY"; fail "stock's GREEN apply did not create exactly ${EXPECTED} resources"; }
GREEN_INV_N="$(inventory "$GREEN_ENDPOINT" | grep -c . || true)"
[ "$GREEN_INV_N" -gt 0 ] || fail "the GREEN inventory reports 0 objects right after a successful apply"
log "  $(grep -E 'Apply complete' <<< "$GREEN_APPLY"), $GREEN_INV_N objects enumerated"

LEFTOVER="$(awsg iam create-role --role-name "${PREFIX}-control-role" \
  --assume-role-policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}' \
  --query 'Role.RoleName' --output text)" || fail "control: could not create the deliberately-added role"
grep -qF "$LEFTOVER" <<< "$(inventory "$GREEN_ENDPOINT")" \
  || fail "control failed: $LEFTOVER was created but the inventory check did not see it - it is not measuring anything"
awsg iam delete-role --role-name "$LEFTOVER" >/dev/null 2>&1 || true
log "  BREAK proved: a deliberately-added role was correctly seen by the inventory check"

GREEN_DESTROY="$(cd "$STOCKGREEN" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform destroy -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GREEN_DESTROY" | grep -E '^Error|^│' | head -30
  fail "stock terraform destroy failed in GREEN"; }
grep -qE "Destroy complete! Resources: ${EXPECTED} destroyed" <<< "$GREEN_DESTROY" \
  || { grep -E 'Destroy complete' <<< "$GREEN_DESTROY"; fail "stock's destroy did not remove exactly ${EXPECTED} resources"; }
GREEN_AFTER="$(inventory "$GREEN_ENDPOINT")"
[ -z "$GREEN_AFTER" ] || { printf '%s\n' "$GREEN_AFTER"; fail "the GREEN account is not empty after terraform destroy: $GREEN_AFTER"; }
log "  $(grep -E 'Destroy complete' <<< "$GREEN_DESTROY"); the GREEN account is enumerated empty, ready for the greenfield apply"

gauntlet_stage cold_deploy pass "stock terraform applied ${EXPECTED} resources at scale=${SCALE} from unmodified terralith-gen output into BOTH accounts (COLD keeps its terraform.tfstate for migrate to adopt from and is confirmed carrying no tofu-address tag; GREEN was enumerated at ${GREEN_INV_N} objects, proved non-vacuous by a deliberately-added role, then destroyed back to an enumerated-empty account - issue #564's own proof, unchanged)"

# ══════════════════════════════════════════════════════════════════════════
# PART B: THE STOCK ORACLES, computed BEFORE any marker is written
# ══════════════════════════════════════════════════════════════════════════
#
# Each of these is a plan (or, for drift, a plan and its reconverging apply)
# over a COPY of cold_deploy's own directory - same state file, same
# provider, `cp -r` so no re-init is needed - with one configuration edit.
# They report no verdict of their own; gauntlet_begin_stage names the stage
# each belongs to so a failure here is attributed to the stage it would have
# been the oracle for, and gauntlet_end_stage closes the window afterwards.

# oracle_dir sets ODIR to a fresh copy of COLD (state file, .terraform and
# all, so no re-init is needed) with the named edits applied. It sets a
# global rather than echoing a path on purpose: a `$(...)` here would run
# render_config - and every fail() inside it - in a SUBSHELL, where `exit 1`
# ends the subshell and lets this script sail on past a real failure with an
# empty variable. That is the "a check that cannot fail is not a check"
# shape, one level down.
ODIR=""
oracle_dir() { # $1 = suffix, rest = edits
  local suffix="$1"; shift
  ODIR="$WORK/oracle-$suffix"
  cp -r "$COLD" "$ODIR" || fail "could not copy COLD for the $suffix oracle"
  render_config "$ODIR" "$@"
}

gauntlet_begin_stage day2_rename
log "=== B1. day2_rename stock oracle: the same two renames, through moved blocks, on cold_deploy's own state ==="
oracle_dir rename rename0 moved0 rename1 moved1
O_RENAME="$ODIR"
O_RENAME_PLAN="$(cd "$O_RENAME" && AWS_ENDPOINT_URL="$ENDPOINT" terraform plan -input=false -no-color 2>&1)"; O_RC=$?
[ "$O_RC" -eq 0 ] || { printf '%s\n' "$O_RENAME_PLAN" | tail -30; fail "the day2_rename stock oracle plan exited $O_RC"; }
grep -qE '^  # .+ will be destroyed' <<< "$O_RENAME_PLAN" \
  && { grep -E '^  # .+ will be' <<< "$O_RENAME_PLAN"; fail "stock proposes a destroy for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qE '^  # .+ will be created' <<< "$O_RENAME_PLAN" \
  && { grep -E '^  # .+ will be' <<< "$O_RENAME_PLAN"; fail "stock proposes a create for a rename carried entirely by moved blocks - the oracle itself is not zero-churn"; }
grep -qE '^  # aws_iam_instance_profile\.team_0000_profile has moved to aws_iam_instance_profile\.team_0000_profile_renamed' <<< "$O_RENAME_PLAN" \
  || { printf '%s\n' "$O_RENAME_PLAN" | tail -20; fail "stock's plan does not report the team_0000_profile move"; }
plan_is_noop "$O_RENAME_PLAN" \
  || { printf '%s\n' "$O_RENAME_PLAN" | tail -12; fail "stock's rename plan is not a true no-op"; }
log "  stock: zero churn, both profiles report only their move, on the state cold_deploy produced"
gauntlet_end_stage

gauntlet_begin_stage day2_remove
log "=== B2. day2_remove stock oracle: delete one taggable block and one untaggable child, on cold_deploy's own state ==="
oracle_dir remove remove2
O_REMOVE="$ODIR"
O_REMOVE_PLAN="$(cd "$O_REMOVE" && AWS_ENDPOINT_URL="$ENDPOINT" terraform plan -input=false -no-color 2>&1)"; O_RC=$?
[ "$O_RC" -eq 0 ] || { printf '%s\n' "$O_REMOVE_PLAN" | tail -30; fail "the day2_remove stock oracle plan exited $O_RC"; }
grep -qE '^  # aws_iam_instance_profile\.team_0002_profile will be destroyed' <<< "$O_REMOVE_PLAN" \
  || { grep -E '^  # .+ will be' <<< "$O_REMOVE_PLAN"; fail "stock does not propose destroying aws_iam_instance_profile.team_0002_profile when its block is removed"; }
grep -qE '^  # aws_iam_role_policy\.team_0002_inline will be destroyed' <<< "$O_REMOVE_PLAN" \
  || { grep -E '^  # .+ will be' <<< "$O_REMOVE_PLAN"; fail "stock does not propose destroying aws_iam_role_policy.team_0002_inline (the untaggable child whose parent role stays) when its block is removed"; }
grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$O_REMOVE_PLAN" \
  || { printf '%s\n' "$O_REMOVE_PLAN" | tail -12; fail "stock's remove plan proposes something other than exactly two destroys"; }
log "  stock: exactly two destroys (the instance profile and the untaggable inline policy), the parent role untouched"
gauntlet_end_stage

gauntlet_begin_stage day2_replace
log "=== B3. day2_replace stock oracle: change team_0004_profile's ForceNew name under create_before_destroy ==="
oracle_dir replace replace4
O_REPLACE="$ODIR"
O_REPLACE_PLAN="$(cd "$O_REPLACE" && AWS_ENDPOINT_URL="$ENDPOINT" terraform plan -input=false -no-color 2>&1)"; O_RC=$?
[ "$O_RC" -eq 0 ] || { printf '%s\n' "$O_REPLACE_PLAN" | tail -40; fail "the day2_replace stock oracle plan exited $O_RC"; }
grep -qE '^  # aws_iam_instance_profile\.team_0004_profile must be replaced' <<< "$O_REPLACE_PLAN" \
  || { grep -E '^  # .+ (will be|must be)' <<< "$O_REPLACE_PLAN"; fail "stock does not propose replacing aws_iam_instance_profile.team_0004_profile when its ForceNew name argument changes"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$O_REPLACE_PLAN" \
  || { printf '%s\n' "$O_REPLACE_PLAN" | tail -12; fail "the day2_replace stock oracle plan is not exactly one isolated replace"; }
log "  stock: exactly one isolated replace at the same declared address - 1 to add, 1 to destroy, nothing cascading"
gauntlet_end_stage

# drift's oracle is the one that has to WRITE, because the mutation is the
# experiment: stock cannot plan against a drift that has not happened. It
# mutates, plans, and then applies its own fix, which restores the account
# to exactly the state cold_deploy left - asserted below, not assumed - so
# nothing downstream inherits a tampered tag.
gauntlet_begin_stage drift_reconverge
log "=== B4. drift_reconverge stock oracle: mutate the VPC's Name tag out of band, plan, reconverge ==="
VPC_ID="$(awsl ec2 describe-vpcs --filters "Name=tag:Name,Values=${PREFIX}-vpc" --query 'Vpcs[0].VpcId' --output text)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "no live VPC found by its Name tag before the drift oracle"
awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Name,Value=tampered-for-the-stock-oracle >/dev/null \
  || fail "the drift oracle's out-of-band tag mutation failed"
O_DRIFT_PLAN="$(cd "$COLD" && AWS_ENDPOINT_URL="$ENDPOINT" terraform plan -input=false -no-color 2>&1)"; O_RC=$?
[ "$O_RC" -eq 0 ] || { printf '%s\n' "$O_DRIFT_PLAN" | tail -30; fail "the drift_reconverge stock oracle plan exited $O_RC"; }
O_DRIFT_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$O_DRIFT_PLAN" | awk '{print $2}' | sort -u)"
[ "$O_DRIFT_ADDRS" = "aws_vpc.main" ] \
  || { grep -E '^  # .+ will be' <<< "$O_DRIFT_PLAN"; fail "stock's plan after the same one-object mutation proposes fixing [$O_DRIFT_ADDRS], not exactly aws_vpc.main - the oracle itself does not scope to one object"; }
O_DRIFT_APPLY="$(cd "$COLD" && AWS_ENDPOINT_URL="$ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$O_DRIFT_APPLY" | tail -30; fail "the drift oracle's reconverging stock apply failed"; }
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$O_DRIFT_APPLY" \
  || { grep -E 'Apply complete' <<< "$O_DRIFT_APPLY"; fail "stock's reconverging apply did not change exactly 1 resource"; }
O_DRIFT_NAME="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" --query 'Tags[0].Value' --output text)"
[ "$O_DRIFT_NAME" = "${PREFIX}-vpc" ] \
  || fail "stock's reconverge left the VPC's Name tag as \"$O_DRIFT_NAME\", not ${PREFIX}-vpc - the account is not back where cold_deploy left it"
log "  stock: exactly one object proposed (aws_vpc.main), reconverged, Name tag restored to ${PREFIX}-vpc"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART C: MIGRATE
# ══════════════════════════════════════════════════════════════════════════

gauntlet_begin_stage migrate
log "=== C1. migrate: the SAME estate with a live block, adopting cold_deploy's state file ==="
render_config "$ADOPTED" live
( cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "choudoufu init failed in the adopted directory"; }

# The oracle live/GAUNTLET.md names for this stage is "the stock state
# file's instance list; every address in it must be accounted for by name".
# `terraform state list` IS that list, printed by stock itself.
STOCK_ADDRS="$(cd "$COLD" && AWS_ENDPOINT_URL="$ENDPOINT" terraform state list 2>/dev/null | sort)"
STOCK_ADDR_N="$(grep -c . <<< "$STOCK_ADDRS" || true)"
[ "$STOCK_ADDR_N" = "$EXPECTED" ] \
  || fail "stock's own state list names $STOCK_ADDR_N addresses, not the ${EXPECTED} this estate declares - the oracle for this stage is not what it should be"
log "  stock's state names $STOCK_ADDR_N addresses; every one must be accounted for by live-import"

MIGRATE_EXPECT="$TAGGABLE"
if [ "${BREAK_MIGRATE:-}" = "1" ]; then
  MIGRATE_EXPECT=$((TAGGABLE - 1))
  log "  BREAK_MIGRATE=1: expecting ${MIGRATE_EXPECT} eligible instances instead of ${TAGGABLE} - the assertion below must fail"
fi

IMPORT_OUT="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" live-import -state="$COLD/terraform.tfstate" -estate="$ESTATE" 2>&1)" || {
  printf '%s\n' "$IMPORT_OUT" | tail -60; fail "live-import (read-only ratification) failed"; }
printf '%s\n' "$IMPORT_OUT" > "$WORK/migrate_dryrun.out"
if grep -qF "${MIGRATE_EXPECT} of ${EXPECTED} resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT"; then
  [ "${BREAK_MIGRATE:-}" = "1" ] && fail "BREAK_MIGRATE=1: live-import reported ${MIGRATE_EXPECT} eligible, so removing one from the expected count did NOT make the assertion fail - this stage's check is not load-bearing"
  log "  ratified: ${MIGRATE_EXPECT} of ${EXPECTED} eligible for stamping"
else
  if [ "${BREAK_MIGRATE:-}" = "1" ]; then
    log "  BREAK_MIGRATE=1: the summary-line assertion correctly failed with the count off by one: $(grep -E 'eligible for stamping' <<< "$IMPORT_OUT" | head -1)"
    not_run_rest "BREAK_MIGRATE=1 control run: this run exists to prove migrate's own count assertion is load-bearing and stops once it has" \
      migrate test_plan test_apply greenfield drift_reconverge day2_rename day2_remove day2_count day2_replace strict
    gauntlet_end
    exit 0
  fi
  grep -E 'eligible for stamping' <<< "$IMPORT_OUT" | head -3
  fail "live-import did not ratify ${TAGGABLE} of ${EXPECTED} instances as eligible - see $WORK/migrate_dryrun.out"
fi

# Every address stock's state names must appear, by name, in the report.
MISSING_ADDRS=""
while IFS= read -r addr; do
  [ -n "$addr" ] || continue
  grep -qF "$addr" <<< "$IMPORT_OUT" || MISSING_ADDRS="${MISSING_ADDRS}${MISSING_ADDRS:+ }$addr"
done <<< "$STOCK_ADDRS"
[ -z "$MISSING_ADDRS" ] \
  || fail "live-import's report does not account for $(wc -w <<< "$MISSING_ADDRS" | tr -d ' ') address(es) stock's own state names: $MISSING_ADDRS"
log "  every one of the $STOCK_ADDR_N addresses in stock's state is named in live-import's report"

APPROVE_OUT="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" live-import -state="$COLD/terraform.tfstate" -estate="$ESTATE" -approve 2>&1)" || {
  printf '%s\n' "$APPROVE_OUT" | tail -60; fail "live-import -approve failed"; }
printf '%s\n' "$APPROVE_OUT" > "$WORK/migrate_approve.out"
grep -qE "${TAGGABLE} resource\(s\) newly stamped" <<< "$APPROVE_OUT" \
  || { tail -20 <<< "$APPROVE_OUT"; fail "live-import -approve did not report ${TAGGABLE} resources newly stamped - see $WORK/migrate_approve.out"; }
grep -qE ', 0 failed, ' <<< "$APPROVE_OUT" \
  || { tail -20 <<< "$APPROVE_OUT"; fail "live-import -approve reported a non-zero failure count - see $WORK/migrate_approve.out"; }
grep -qE "${UNTAGGABLE} skipped" <<< "$APPROVE_OUT" \
  || { tail -20 <<< "$APPROVE_OUT"; fail "live-import -approve did not report exactly ${UNTAGGABLE} skipped (the untaggable instances whose identity composes from an already-stamped parent) - see $WORK/migrate_approve.out"; }
log "  $(grep -E 'newly stamped' <<< "$APPROVE_OUT" | head -1)"
gauntlet_stage migrate pass "live-import ratified ${TAGGABLE} of ${EXPECTED} instances as eligible and stamped all ${TAGGABLE} with 0 failed and ${UNTAGGABLE} skipped (untaggable, identity composed from an already-stamped parent); every one of the ${STOCK_ADDR_N} addresses in stock's own \`terraform state list\` - this stage's oracle - is accounted for by name in the report"

# ══════════════════════════════════════════════════════════════════════════
# PART D: REPLAN FROM NOTHING (test_plan)
# ══════════════════════════════════════════════════════════════════════════

gauntlet_begin_stage test_plan
log "=== D1. test_plan: the post-migration plan must be empty ==="
PLAN_OUT="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || {
  printf '%s\n' "$PLAN_OUT" | tail -40
  PLAN_ERR="$(grep -m1 -E '^Error: ' <<< "$PLAN_OUT" | tr -d '\r')"
  PLAN_RULE="$(grep -m1 -oE 'Rule: [a-z0-9-]+' <<< "$PLAN_OUT")"
  fail "the post-migration plan exited ${PLAN_RC}${PLAN_RULE:+, ${PLAN_RULE}}${PLAN_ERR:+ - first: ${PLAN_ERR}}"; }
plan_is_noop "$PLAN_OUT" \
  || { grep -E '^  #' <<< "$PLAN_OUT" | head -20; fail "the post-migration plan is not empty"; }
log "  No changes."

log "=== D2. test_plan: rendered identities asserted BY VALUE against the AWS CLI ==="
if [ "${BREAK_PLAN:-}" = "1" ]; then
  BAD='aws_iam_role.count_team[9]'
  BREAK_MIS="$(identity_mismatches "$ENDPOINT" "$BAD")"
  BREAK_N="$(grep -c . <<< "$BREAK_MIS" || true)"
  [ "$BREAK_N" = "1" ] \
    || { printf '%s\n' "$BREAK_MIS"; fail "BREAK_PLAN=1: corrupting one expected identity string produced $BREAK_N mismatch(es), not exactly 1 - this stage's identity check is not load-bearing, or it is not scoped to the string that was corrupted"; }
  grep -qF "want=$(escape_address "$BAD")" <<< "$BREAK_MIS" \
    || { printf '%s\n' "$BREAK_MIS"; fail "BREAK_PLAN=1: the single mismatch is not the corrupted string"; }
  log "  BREAK_PLAN=1: exactly one mismatch, and it is the corrupted string ($BAD) - the identity assertion fails on that string and nothing else, as it must"
  not_run_rest "BREAK_PLAN=1 control run: this run exists to prove test_plan's identity assertion is load-bearing and stops once it has" \
    test_plan test_apply greenfield drift_reconverge day2_rename day2_remove day2_count day2_replace strict
  gauntlet_end
  exit 0
fi
MIS="$(identity_mismatches "$ENDPOINT")"
[ -z "$MIS" ] || { printf '%s\n' "$MIS"; fail "one or more rendered identities do not match what the AWS CLI reports for the same live object"; }
log "  six identities confirmed by value through four different AWS tagging surfaces:"
log "    aws_route53_zone.main (Route 53), aws_iam_role.team_0000_role (IAM),"
log "    aws_iam_role.count_team[1] (count-indexed), module.team_pod[\"pod-a\"].aws_iam_role.pod_role[0] (module-nested, double-indexed),"
log "    aws_ecs_cluster.main (ECS), aws_vpc.main (EC2)"
gauntlet_stage test_plan pass "post-migration plan is empty; six rendered identities asserted BY VALUE against the AWS CLI across four separate tagging surfaces (Route 53, IAM, ECS, EC2), including the count-indexed aws_iam_role.count_team[1] and the module-nested, double-indexed module.team_pod[\"pod-a\"].aws_iam_role.pod_role[0]"

# ══════════════════════════════════════════════════════════════════════════
# PART E: NO-OP APPLY (test_apply)
# ══════════════════════════════════════════════════════════════════════════

gauntlet_begin_stage test_apply
log "=== E1. test_apply: applying the empty plan changes nothing ==="
INV_BEFORE="$(inventory "$ENDPOINT")"
INV_BEFORE_N="$(grep -c . <<< "$INV_BEFORE" || true)"
[ "$INV_BEFORE_N" -gt 0 ] || fail "the pre-apply inventory is empty - this comparison would be vacuous"
TAGGED_BEFORE="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo unknown)"
NOOP_OUT="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; NOOP_RC=$?
[ "$NOOP_RC" -eq 0 ] || { printf '%s\n' "$NOOP_OUT" | tail -30; fail "the no-op apply exited $NOOP_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$NOOP_OUT" \
  || { grep -E 'Apply complete|No changes' <<< "$NOOP_OUT"; fail "the no-op apply was not a genuine no-op"; }
INV_AFTER="$(inventory "$ENDPOINT")"
if [ "${BREAK_APPLY:-}" = "1" ]; then
  INV_BEFORE="$INV_BEFORE
${PREFIX}-an-object-that-was-never-there"
  log "  BREAK_APPLY=1: expecting a different inventory (one extra object) - the unchanged assertion below must fail"
  if [ "$INV_BEFORE" = "$INV_AFTER" ]; then
    fail "BREAK_APPLY=1: expecting a different inventory still compared equal - this stage's check is not load-bearing"
  fi
  log "  BREAK_APPLY=1: the inventory comparison correctly failed against a deliberately wrong expectation"
  not_run_rest "BREAK_APPLY=1 control run: this run exists to prove test_apply's inventory assertion is load-bearing and stops once it has" \
    test_apply greenfield drift_reconverge day2_rename day2_remove day2_count day2_replace strict
  gauntlet_end
  exit 0
fi
[ "$INV_BEFORE" = "$INV_AFTER" ] \
  || { diff <(printf '%s\n' "$INV_BEFORE") <(printf '%s\n' "$INV_AFTER") || true; fail "the enumerated estate changed across a no-op apply"; }
TAGGED_AFTER="$(awsl resourcegroupstaggingapi get-resources --tag-filters "Key=tofu-estate,Values=$ESTATE" --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo unknown)"
[ "$TAGGED_BEFORE" = "$TAGGED_AFTER" ] \
  || fail "the tofu-estate-tagged object count changed across a no-op apply: $TAGGED_BEFORE -> $TAGGED_AFTER"
log "  genuine no-op: $INV_BEFORE_N objects enumerated identically before and after, and the tofu-estate-tagged count is unchanged at $TAGGED_AFTER"
gauntlet_stage test_apply pass "no-op apply (0 added, 0 changed, 0 destroyed); the estate is enumerated object by object before and after - $INV_BEFORE_N objects across IAM/Route53/ECS/EC2, byte-identical listings, never a bare count - and the tofu-estate-tagged count is unchanged at $TAGGED_AFTER"

# ══════════════════════════════════════════════════════════════════════════
# PART F: GREENFIELD
# ══════════════════════════════════════════════════════════════════════════
#
# GREEN is enumerated empty (part A2 proved it, with a stock destroy). This
# is choudoufu applying the same configuration into it directly, no
# migration anywhere, and then the stage's own oracle: the resulting cloud
# compared object by object with the cloud stock's cold deploy produced in
# COLD, marker tags never read on either side.

gauntlet_begin_stage greenfield
log "=== F1. greenfield: choudoufu applies the same configuration into the empty GREEN account ==="
render_config "$GREENDIR" live
( cd "$GREENDIR" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) \
  || { ( cd "$GREENDIR" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" init -input=false -no-color 2>&1 | tail -20 ); fail "choudoufu init failed in the greenfield directory"; }
GF_APPLY="$(cd "$GREENDIR" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$GF_APPLY" | grep -E '^Error|^│' | head -30
  fail "the greenfield apply failed"; }
grep -qE "Apply complete! Resources: ${EXPECTED} added, 0 changed, 0 destroyed" <<< "$GF_APPLY" \
  || { grep -E 'Apply complete' <<< "$GF_APPLY"; fail "the greenfield apply did not create exactly ${EXPECTED} resources"; }
log "  $(grep -E 'Apply complete' <<< "$GF_APPLY")"

log "=== F2. greenfield: markers written on create, asserted BY VALUE through the AWS CLI ==="
GF_MIS="$(identity_mismatches "$GREEN_ENDPOINT")"
[ -z "$GF_MIS" ] || { printf '%s\n' "$GF_MIS"; fail "the greenfield apply did not write the expected identities - read via the AWS CLI, not choudoufu's own report"; }
log "  the same six identities confirmed by value in the greenfield account"

# F3 compares the record store against stock's own instance list per
# resource type rather than against a hard-coded total, because the answer
# is not "one per instance" and a bare number would hide which instance is
# missing. Measured, not assumed (this script's own third run, and
# reproduced standalone against a fresh emulator with no terraform in the
# loop for the plan half): an apply of this estate persists a record for
# every managed instance EXCEPT aws_ecs_task_definition, whose row in
# internal/live/identity/table_generated.go is ServerAssigned with
# IdentityAttrs family+revision - an identity ECS mints anew on every
# register. Nothing warns about it, and nothing in this estate is observably
# worse for it: the marker IS written on the task definition (confirmed
# directly through `ecs list-tags-for-resource`), the plan is empty, and the
# one thing that does move when the record store is deleted is
# aws_ecs_service's residue, not the task definition (F5 below).
#
# So this is reported, not endorsed, and the assertion is written to fail
# loudly if the gap ever changes shape - a different type joining it, or
# this one leaving it - rather than to encode a total that would go on
# passing either way.
log "=== F3. greenfield: the record store, compared per type against stock's own instance list ==="
GF_REC_BASE="$GREENDIR/.tofu-records/tofu-records/$ESTATE"
[ -d "$GF_REC_BASE" ] || fail "the greenfield apply left no record store at $GF_REC_BASE"
GF_EXP_TYPES="$(printf '%s\n' "$STOCK_ADDRS" | addr_type_counts)"
GF_ACT_TYPES="$(record_type_counts "$GF_REC_BASE")"
GF_UNRECORDED="$(comm -23 <(printf '%s\n' "$GF_EXP_TYPES") <(printf '%s\n' "$GF_ACT_TYPES"))"
GF_EXTRA="$(comm -13 <(printf '%s\n' "$GF_EXP_TYPES") <(printf '%s\n' "$GF_ACT_TYPES"))"
[ -z "$GF_EXTRA" ] \
  || { printf '%s\n' "$GF_EXTRA"; fail "the record store holds records for a type or a count stock's own instance list does not name"; }
GF_TD_N="$(awk '$1=="aws_ecs_task_definition"{print $2}' <<< "$GF_EXP_TYPES")"
[ -n "$GF_TD_N" ] || fail "stock's instance list names no aws_ecs_task_definition - this estate's composition changed and F3's known gap needs re-measuring"
[ "$GF_UNRECORDED" = "aws_ecs_task_definition $GF_TD_N" ] \
  || { printf 'unrecorded types:\n%s\n' "$GF_UNRECORDED"; fail "the set of instances with no record is not exactly the ${GF_TD_N} aws_ecs_task_definition instance(s) this estate's known gap names - re-measure before trusting either side"; }
GF_RECORDS="$(find "$GF_REC_BASE" -type f ! -name '*.lock' ! -name '*.tmp-*' | wc -l | tr -d ' ')"
[ "$GF_RECORDS" = "$((EXPECTED - GF_TD_N))" ] \
  || fail "the per-type comparison agreed but the record total is $GF_RECORDS, not $((EXPECTED - GF_TD_N))"
log "  $GF_RECORDS records persisted, matching stock's instance list type for type in every type but one: aws_ecs_task_definition (${GF_TD_N} instance(s)) gets no record - reported, not endorsed"

log "=== F4. greenfield: the next plan proposes nothing ==="
GF_PLAN="$(cd "$GREENDIR" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GF_RC=$?
[ "$GF_RC" -eq 0 ] || { printf '%s\n' "$GF_PLAN" | tail -30; fail "the greenfield replan exited $GF_RC"; }
plan_is_noop "$GF_PLAN" \
  || { grep -E '^  #' <<< "$GF_PLAN" | head -20; fail "the greenfield replan is not empty"; }
log "  No changes."

# F5 deletes the whole local record store and replans. What that proves,
# and what it deliberately does NOT demand:
#
#   Every one of the ${EXPECTED} objects must still be FOUND - nothing may
#   be proposed for create, destroy or replace. That is the real question,
#   and it covers the ${UNTAGGABLE} untaggable instances, which have no
#   marker of their own and must compose their identity from an
#   already-stamped parent.
#
#   An in-place update on aws_ecs_service is expected and is not a failure.
#   The record store is also the RESIDUE store (internal/live/projection/
#   residue.go, issue #275): it holds argument values the provider's own
#   Read never returns, so that a cold replan does not propose re-sending
#   them forever. aws_ecs_service has three - deployment_maximum_percent,
#   deployment_minimum_healthy_percent and wait_for_steady_state, the last
#   being a pure client-side wait flag AWS never stores at all - and
#   deleting the store is deleting the only place they were written down.
#   Demanding an empty plan here would be demanding that the residue store
#   not exist. live/e2e/reference-ec2-vpc/run.sh can demand it only because
#   none of its five types carries residue.
log "=== F5. greenfield: delete the local record store entirely and plan again ==="
rm -rf "$GREENDIR/.tofu-records"
GF_PLAN2="$(cd "$GREENDIR" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; GF_RC=$?
[ "$GF_RC" -eq 0 ] || { printf '%s\n' "$GF_PLAN2" | tail -40; fail "the greenfield plan with no local record store exited $GF_RC"; }
grep -qE '^  # .+ will be created' <<< "$GF_PLAN2" \
  && { grep -E '^  # .+ will be' <<< "$GF_PLAN2" | head -20; fail "with no local record store the plan proposes CREATING something that already exists - an object is not being found by its marker, and a create is the failure mode a wrong or missing identity produces"; }
grep -qE '^  # .+ will be destroyed' <<< "$GF_PLAN2" \
  && { grep -E '^  # .+ will be' <<< "$GF_PLAN2" | head -20; fail "with no local record store the plan proposes destroying something the configuration still declares"; }
grep -qE '^  # .+ must be replaced' <<< "$GF_PLAN2" \
  && { grep -E '^  # .+ (will be|must be)' <<< "$GF_PLAN2" | head -20; fail "with no local record store the plan proposes replacing something the configuration still declares"; }
GF_NOREC_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$GF_PLAN2" | awk '{print $2}' | sort -u)"
GF_NOREC_OTHER="$(grep -v '^aws_ecs_service\.' <<< "$GF_NOREC_ADDRS" 2>/dev/null || true)"
[ -z "$GF_NOREC_OTHER" ] \
  || { printf '%s\n' "$GF_NOREC_OTHER"; fail "with no local record store the plan proposes in-place updates outside aws_ecs_service, which is the only type in this estate whose arguments the provider's Read does not return - see internal/live/projection/residue.go"; }
GF_NOREC_N="$(grep -c . <<< "$GF_NOREC_ADDRS" || true)"
log "  every one of the ${EXPECTED} objects still found with zero local memory of the run that created them - nothing created, destroyed or replaced; ${TAGGABLE} found by their own marker, ${UNTAGGABLE} composed from an already-stamped parent; the only movement is ${GF_NOREC_N} residue-held aws_ecs_service update(s), which is what deleting the residue store means"

log "=== F6. greenfield oracle: choudoufu's cloud against stock's cold deploy, object by object ==="
GF_SHAPE="$(shape "$GREEN_ENDPOINT")"
COLD_SHAPE="$(shape "$ENDPOINT")"
GF_SHAPE_N="$(grep -c . <<< "$GF_SHAPE" || true)"
[ "$GF_SHAPE_N" -gt 0 ] || fail "the greenfield structural inventory is empty - the comparison would be vacuous"
if [ "${BREAK_GREENFIELD:-}" = "1" ]; then
  GF_SHAPE="$(grep -v '^record ' <<< "$GF_SHAPE")"
  log "  BREAK_GREENFIELD=1: dropped every Route 53 record from the expected inventory - the comparison below must fail"
  if [ "$GF_SHAPE" = "$COLD_SHAPE" ]; then
    fail "BREAK_GREENFIELD=1: dropping a whole resource kind from the expected inventory should have made the comparison fail, but it still matched - this stage's check is not load-bearing"
  fi
  log "  BREAK_GREENFIELD=1: correctly mismatched with one resource kind dropped"
  not_run_rest "BREAK_GREENFIELD=1 control run: this run exists to prove greenfield's object-by-object comparison is load-bearing and stops once it has" \
    greenfield drift_reconverge day2_rename day2_remove day2_count day2_replace strict
  gauntlet_end
  exit 0
fi
if [ "$GF_SHAPE" != "$COLD_SHAPE" ]; then
  diff <(printf '%s\n' "$GF_SHAPE") <(printf '%s\n' "$COLD_SHAPE") || true
  fail "the greenfield cloud does not match stock's cold deploy, object by object"
fi
log "  object-by-object match across $GF_SHAPE_N structural facts: IAM role names and every role's inline policies and attachments, customer-managed policy names, instance-profile names and the role each holds, VPC cidr, subnet cidr and AZ, security-group egress rules, ECS cluster, service (name/desired/launch type) and task-definition family+revision, the hosted zone and all its records (name/type/ttl/value) - marker tags never read on either side"
gauntlet_stage greenfield pass "choudoufu applied ${EXPECTED} resources into an account a stock destroy had left enumerated empty (A2), and its cloud matches stock's cold deploy across $GF_SHAPE_N structural facts compared object by object with marker tags never read on either side - the oracle this stage names. Also, beyond the oracle: the six representative identities are correct by value via the AWS CLI across Route 53/IAM/ECS/EC2; the apply persisted $GF_RECORDS records, matching stock's own instance list type for type except for the ${GF_TD_N} aws_ecs_task_definition instance(s), which get none (reported, not endorsed - the marker IS written on it and nothing here is observably worse for it); the next plan is empty; and with the local record store deleted outright every one of the ${EXPECTED} objects is still found - nothing created, destroyed or replaced, ${UNTAGGABLE} of them untaggable and composing from a stamped parent - with the only movement being ${GF_NOREC_N} residue-held aws_ecs_service update(s), which is what deleting the residue store (issue #275) means rather than a divergence"

# ══════════════════════════════════════════════════════════════════════════
# PART G: day2_count's STOCK ORACLE, in the now-idle GREEN account
# ══════════════════════════════════════════════════════════════════════════
#
# The other day-2 oracles are plans over cold_deploy's own state, but a
# count cycle's second half (scaling back UP) cannot be planned from a state
# that is still at the original count - stock has to actually apply the
# scale-down first. GREEN is idle from here on (the greenfield oracle above
# is the last thing that reads it), so this applies a separately-prefixed
# fixture there for real: the SAME six-block count_team shape
# tools/terralith-gen emits, under prefix ${PREFIX}o, which collides with
# nothing the greenfield estate declares.

count_oracle_block() { # $1 = count
  cat <<EOF
resource "aws_iam_role" "count_team" {
  count = $1
  name  = "${PREFIX}o-count-team-\${format("%04d", count.index)}-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Action    = "sts:AssumeRole"
      Principal = { Service = "ec2.amazonaws.com" }
    }]
  })
}

resource "aws_iam_role_policy" "count_team_inline" {
  count = $1
  name  = "${PREFIX}o-count-team-\${format("%04d", count.index)}-inline"
  role  = aws_iam_role.count_team[count.index].name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:GetObject", "s3:ListBucket"]
      Resource = "*"
    }]
  })
}

resource "aws_iam_policy" "count_team_policy" {
  count = $1
  name  = "${PREFIX}o-count-team-\${format("%04d", count.index)}-policy"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:*"]
      Resource = "arn:aws:s3:::terralith-shared-bucket/*"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "count_team_managed_attach" {
  count      = $1
  role       = aws_iam_role.count_team[count.index].name
  policy_arn = "arn:aws:iam::aws:policy/ReadOnlyAccess"
}

resource "aws_iam_role_policy_attachment" "count_team_custom_attach" {
  count      = $1
  role       = aws_iam_role.count_team[count.index].name
  policy_arn = aws_iam_policy.count_team_policy[count.index].arn
}

resource "aws_iam_instance_profile" "count_team_profile" {
  count = $1
  name  = "${PREFIX}o-count-team-\${format("%04d", count.index)}-profile"
  role  = aws_iam_role.count_team[count.index].name
}
EOF
}

write_count_oracle() { # $1 = dir, $2 = count
  {
    cat "$GEN/versions.tf"
    echo
    count_oracle_block "$2"
  } > "$1/main.tf"
}

gauntlet_begin_stage day2_count
log "=== G1. day2_count stock oracle: apply the same six-block count fixture at count=2 in the idle GREEN account ==="
OCOUNT="$WORK/oracle-count"
mkdir -p "$OCOUNT"
write_count_oracle "$OCOUNT" 2
( cd "$OCOUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform init -input=false -no-color >/dev/null 2>&1 ) \
  || fail "the day2_count oracle's terraform init failed"
OC_APPLY="$(cd "$OCOUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$OC_APPLY" | tail -30; fail "the day2_count oracle's baseline apply failed"; }
grep -qE 'Apply complete! Resources: 12 added' <<< "$OC_APPLY" \
  || { grep -E 'Apply complete' <<< "$OC_APPLY"; fail "the day2_count oracle's baseline apply did not create exactly 12 resources (6 blocks x count 2)"; }
OC_ROLE0="$(awsg iam get-role --role-name "${PREFIX}o-count-team-0000-role" --query 'Role.RoleId' --output text)"
[ -n "$OC_ROLE0" ] && [ "$OC_ROLE0" != "None" ] || fail "the day2_count oracle's count_team[0] role does not exist after the baseline apply"

write_count_oracle "$OCOUNT" 1
OC_DOWN_PLAN="$(cd "$OCOUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; O_RC=$?
[ "$O_RC" -eq 0 ] || { printf '%s\n' "$OC_DOWN_PLAN" | tail -30; fail "the day2_count oracle's scale-down plan exited $O_RC"; }
grep -qF 'Plan: 0 to add, 0 to change, 6 to destroy.' <<< "$OC_DOWN_PLAN" \
  || { printf '%s\n' "$OC_DOWN_PLAN" | tail -12; fail "stock's scale-down plan proposes something other than exactly six destroys"; }
grep -qE '^  # \S+\[0\] will be' <<< "$OC_DOWN_PLAN" \
  && { grep -E '^  # .+ will be' <<< "$OC_DOWN_PLAN"; fail "stock's scale-down plan touches an index-[0] instance, which must be untouched"; }
OC_DOWN_N="$(grep -cE '^  # \S+\[1\] will be destroyed' <<< "$OC_DOWN_PLAN" || true)"
[ "$OC_DOWN_N" = "6" ] \
  || { grep -E '^  # .+ will be' <<< "$OC_DOWN_PLAN"; fail "stock's scale-down plan destroys $OC_DOWN_N index-[1] instances, not 6"; }
OC_DOWN_APPLY="$(cd "$OCOUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$OC_DOWN_APPLY" | tail -30; fail "the day2_count oracle's scale-down apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 6 destroyed' <<< "$OC_DOWN_APPLY" \
  || { grep -E 'Apply complete' <<< "$OC_DOWN_APPLY"; fail "the day2_count oracle's scale-down apply was not exactly six destroys"; }
OC_ROLE0_AFTER="$(awsg iam get-role --role-name "${PREFIX}o-count-team-0000-role" --query 'Role.RoleId' --output text)"
[ "$OC_ROLE0_AFTER" = "$OC_ROLE0" ] || fail "stock's surviving count_team[0] role changed identity across the scale-down"
OC_ROLE1_N="$(awsg iam list-roles --query "length(Roles[?RoleName=='${PREFIX}o-count-team-0001-role'])" --output text)"
[ "$OC_ROLE1_N" = "0" ] || fail "stock's count_team[1] role still exists after the scale-down destroy"
log "  stock: exactly six destroys, all index [1]; index [0] identity unchanged, index [1] genuinely gone"

write_count_oracle "$OCOUNT" 2
OC_UP_PLAN="$(cd "$OCOUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform plan -input=false -no-color 2>&1)"; O_RC=$?
[ "$O_RC" -eq 0 ] || { printf '%s\n' "$OC_UP_PLAN" | tail -30; fail "the day2_count oracle's scale-up plan exited $O_RC"; }
grep -qF 'Plan: 6 to add, 0 to change, 0 to destroy.' <<< "$OC_UP_PLAN" \
  || { printf '%s\n' "$OC_UP_PLAN" | tail -12; fail "stock's scale-up plan proposes something other than exactly six creates"; }
grep -qE '^  # \S+\[0\] will be' <<< "$OC_UP_PLAN" \
  && { grep -E '^  # .+ will be' <<< "$OC_UP_PLAN"; fail "stock's scale-up plan touches an index-[0] instance, which must be untouched"; }
OC_UP_APPLY="$(cd "$OCOUNT" && AWS_ENDPOINT_URL="$GREEN_ENDPOINT" terraform apply -input=false -auto-approve -no-color 2>&1)" || {
  printf '%s\n' "$OC_UP_APPLY" | tail -30; fail "the day2_count oracle's scale-up apply failed"; }
grep -qE 'Resources: 6 added, 0 changed, 0 destroyed' <<< "$OC_UP_APPLY" \
  || { grep -E 'Apply complete' <<< "$OC_UP_APPLY"; fail "the day2_count oracle's scale-up apply was not exactly six creates"; }
OC_ROLE0_FINAL="$(awsg iam get-role --role-name "${PREFIX}o-count-team-0000-role" --query 'Role.RoleId' --output text)"
[ "$OC_ROLE0_FINAL" = "$OC_ROLE0" ] || fail "stock's count_team[0] role changed identity across the scale-up"
log "  stock: exactly six creates, all index [1]; index [0] identity unchanged throughout the whole cycle"
gauntlet_end_stage

# ══════════════════════════════════════════════════════════════════════════
# PART H: DRIFT AND RECONVERGE
# ══════════════════════════════════════════════════════════════════════════

gauntlet_begin_stage drift_reconverge
log "=== H1. drift_reconverge: mutate exactly one live object out of band, through the AWS CLI ==="
if [ "${BREAK_DRIFT:-}" = "1" ]; then
  SUBNET_ID="$(awsl ec2 describe-subnets --filters "Name=tag:Name,Values=${PREFIX}-subnet" --query 'Subnets[0].SubnetId' --output text)"
  [ -n "$SUBNET_ID" ] && [ "$SUBNET_ID" != "None" ] || fail "no live subnet found for the BREAK_DRIFT control"
  awsl ec2 create-tags --resources "$SUBNET_ID" --tags Key=Name,Value=tampered-by-BREAK >/dev/null
  log "  BREAK_DRIFT=1: also tampered $SUBNET_ID's Name tag - the plan must now see TWO drifted objects"
fi
awsl ec2 create-tags --resources "$VPC_ID" --tags Key=Name,Value=tampered-out-of-band >/dev/null \
  || fail "the out-of-band tag mutation failed"
DRIFTED="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" --query 'Tags[0].Value' --output text)"
[ "$DRIFTED" = "tampered-out-of-band" ] || fail "the out-of-band tag mutation did not take (Name reads \"$DRIFTED\")"
log "  mutated $VPC_ID's Name tag to \"tampered-out-of-band\" - never through choudoufu"

DRIFT_PLAN="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; DRIFT_RC=$?
[ "$DRIFT_RC" -eq 0 ] || { printf '%s\n' "$DRIFT_PLAN" | tail -30; fail "the drift-detection plan exited $DRIFT_RC"; }
DRIFT_ADDRS="$(grep -oE '^  # \S+ will be updated' <<< "$DRIFT_PLAN" | awk '{print $2}' | sort -u)"
DRIFT_N="$(grep -c . <<< "$DRIFT_ADDRS" || true)"
if [ "${BREAK_DRIFT:-}" = "1" ]; then
  [ "$DRIFT_N" = "1" ] \
    && fail "BREAK_DRIFT=1: two objects were tampered but the plan proposes fixing only 1 - the single-object assertion is not load-bearing"
  log "  BREAK_DRIFT=1: the plan proposes fixing $DRIFT_N objects, correctly more than one - the single-object assertion correctly fails to hold"
  not_run_rest "BREAK_DRIFT=1 control run: this run exists to prove drift_reconverge's single-object assertion is load-bearing and stops once it has" \
    drift_reconverge day2_rename day2_remove day2_count day2_replace strict
  gauntlet_end
  exit 0
fi
[ "$DRIFT_N" = "1" ] \
  || { grep -E '^  # .+ will be' <<< "$DRIFT_PLAN"; fail "expected exactly 1 object proposed for a fix, got $DRIFT_N: $DRIFT_ADDRS"; }
[ "$DRIFT_ADDRS" = "aws_vpc.main" ] || fail "the plan proposes fixing $DRIFT_ADDRS, not aws_vpc.main"
grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$DRIFT_PLAN" \
  || { printf '%s\n' "$DRIFT_PLAN" | tail -12; fail "the drift plan is not exactly one in-place change"; }
log "  the plan proposes fixing exactly one object: aws_vpc.main - the same single object stock's own oracle (B4) proposed for the same mutation"

RECONV="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONV_RC=$?
[ "$RECONV_RC" -eq 0 ] || { printf '%s\n' "$RECONV" | tail -30; fail "the reconverging apply failed"; }
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONV" \
  || { grep -E 'Apply complete' <<< "$RECONV"; fail "the reconverging apply did not change exactly 1 resource"; }
FIXED="$(awsl ec2 describe-tags --filters "Name=resource-id,Values=$VPC_ID" "Name=key,Values=Name" --query 'Tags[0].Value' --output text)"
[ "$FIXED" = "${PREFIX}-vpc" ] || fail "the VPC's Name tag is \"$FIXED\" after reconverging, not ${PREFIX}-vpc"
STILL_MARKED="$(marker_of_vpc "$ENDPOINT")"
[ "$STILL_MARKED" = "aws_vpc.main" ] || fail "the VPC's tofu-address marker reads \"$STILL_MARKED\" after the reconverge, not aws_vpc.main - the fix disturbed the identity"
log "  reconverged: $VPC_ID's Name tag is back to ${PREFIX}-vpc and its tofu-address marker is untouched, both read via the AWS CLI"
gauntlet_stage drift_reconverge pass "one live object mutated out of band through the AWS CLI; choudoufu's next plan proposed fixing exactly aws_vpc.main and nothing else (0 add, 1 change, 0 destroy), matching stock's own plan for the identical mutation on cold_deploy's own state (B4, taken before any marker existed); the apply changed exactly 1 resource, the Name tag reads back as configured and the tofu-address marker is unchanged"

# ══════════════════════════════════════════════════════════════════════════
# PART I: RENAME (day2_rename)
# ══════════════════════════════════════════════════════════════════════════
#
# Two mechanisms on two different resources so a gap in either is visible: a
# `moved` block renames team_0000's instance profile, and `choudoufu live-mv`
# renames team_0001's with no moved block at all. Both are taggable, and
# nothing else in the estate references either one, so the rename is a pure
# address change with no other configuration edit anywhere.

gauntlet_begin_stage day2_rename
log "=== I0. capture the two live objects a rename must not disturb ==="
P0_ID="$(awsl iam get-instance-profile --instance-profile-name "${PREFIX}-team-0000-profile" --query 'InstanceProfile.InstanceProfileId' --output text)"
P1_ID="$(awsl iam get-instance-profile --instance-profile-name "${PREFIX}-team-0001-profile" --query 'InstanceProfile.InstanceProfileId' --output text)"
[ -n "$P0_ID" ] && [ "$P0_ID" != "None" ] || fail "no live instance profile ${PREFIX}-team-0000-profile before the rename"
[ -n "$P1_ID" ] && [ "$P1_ID" != "None" ] || fail "no live instance profile ${PREFIX}-team-0001-profile before the rename"
log "  team_0000_profile=$P0_ID team_0001_profile=$P1_ID"

if [ "${BREAK_RENAME:-}" = "1" ]; then
  log "=== I1 (BREAK_RENAME=1). rename WITHOUT a moved block; a destroy and a create must be proposed ==="
  render_config "$ADOPTED" live rename0
  BR_PLAN="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; BR_RC=$?
  [ "$BR_RC" -eq 0 ] || { printf '%s\n' "$BR_PLAN" | tail -30; fail "the BREAK_RENAME=1 plan exited $BR_RC"; }
  grep -qE '^  # aws_iam_instance_profile\.team_0000_profile will be destroyed' <<< "$BR_PLAN" \
    || { grep -E '^  # .+ will be' <<< "$BR_PLAN"; fail "BREAK_RENAME=1: renaming without a moved block did not propose destroying the old address - this stage's check is not load-bearing"; }
  grep -qE '^  # aws_iam_instance_profile\.team_0000_profile_renamed will be created' <<< "$BR_PLAN" \
    || { grep -E '^  # .+ will be' <<< "$BR_PLAN"; fail "BREAK_RENAME=1: renaming without a moved block did not propose creating the new address - this stage's check is not load-bearing"; }
  log "  BREAK_RENAME=1: correctly proposes a destroy and a create - the zero-churn assertion could not have held"
  not_run_rest "BREAK_RENAME=1 control run: this run exists to prove day2_rename's zero-churn assertion is load-bearing and stops once it has" \
    day2_rename day2_remove day2_count day2_replace strict
  gauntlet_end
  exit 0
fi

log "=== I1. choudoufu, moved block: aws_iam_instance_profile.team_0000_profile -> .team_0000_profile_renamed ==="
render_config "$ADOPTED" live rename0 moved0
MOVED_PLAN="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; MV_RC=$?
[ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MOVED_PLAN" | tail -30; fail "the moved-block rename plan exited $MV_RC"; }
grep -qE 'will be destroyed' <<< "$MOVED_PLAN" \
  && { grep -E '^  # .+ will be' <<< "$MOVED_PLAN"; fail "the moved-block rename proposes a destroy - not zero churn"; }
grep -qE 'will be created' <<< "$MOVED_PLAN" \
  && { grep -E '^  # .+ will be' <<< "$MOVED_PLAN"; fail "the moved-block rename proposes a create - not zero churn"; }
grep -qE '^  # aws_iam_instance_profile\.team_0000_profile_renamed will be updated in-place' <<< "$MOVED_PLAN" \
  || { printf '%s\n' "$MOVED_PLAN" | grep -E '^  # .+ will be'; fail "the moved-block plan does not propose an in-place update to the renamed address"; }
grep -qF 'Plan: 0 to add, 1 to change, 0 to destroy.' <<< "$MOVED_PLAN" \
  || { printf '%s\n' "$MOVED_PLAN" | tail -12; fail "the moved-block rename plan is not exactly one in-place change"; }
grep -qE '"tofu-address" += "aws_iam_instance_profile\.team_0000_profile" -> "aws_iam_instance_profile\.team_0000_profile_renamed"' <<< "$MOVED_PLAN" \
  || { printf '%s\n' "$MOVED_PLAN" | grep -A6 'tofu-address'; fail "the moved-block plan does not show the tofu-address marker being rewritten from the old address to the new one"; }
log "  choudoufu: zero churn, exactly one in-place tags update - the marker rewrite the moved block completes"
MOVED_APPLY="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; MV_RC=$?
[ "$MV_RC" -eq 0 ] || { printf '%s\n' "$MOVED_APPLY" | tail -30; fail "the moved-block rename apply exited $MV_RC"; }
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$MOVED_APPLY" \
  || { grep -E 'Apply complete' <<< "$MOVED_APPLY"; fail "the moved-block rename apply was not exactly one in-place change"; }
P0_AFTER="$(awsl iam get-instance-profile --instance-profile-name "${PREFIX}-team-0000-profile" --query 'InstanceProfile.InstanceProfileId' --output text 2>/dev/null || true)"
[ "$P0_AFTER" = "$P0_ID" ] || fail "the instance profile's live id changed across the rename ($P0_ID -> $P0_AFTER) - it was destroyed and recreated, not renamed"
P0_TAG="$(marker_of_profile "$ENDPOINT" "${PREFIX}-team-0000-profile")"
[ "$P0_TAG" = 'aws_iam_instance_profile.team_0000_profile_renamed' ] \
  || fail "the instance profile carries tofu-address=$P0_TAG after the moved-block rename, not aws_iam_instance_profile.team_0000_profile_renamed"
log "  $P0_ID unchanged, tofu-address now aws_iam_instance_profile.team_0000_profile_renamed - read via the AWS CLI"

log "=== I2. choudoufu, live-mv: aws_iam_instance_profile.team_0001_profile -> .team_0001_profile_renamed, no moved block at all ==="
render_config "$ADOPTED" live rename0 moved0 rename1
LIVEMV_OUT="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" live-mv -estate="$ESTATE" aws_iam_instance_profile.team_0001_profile aws_iam_instance_profile.team_0001_profile_renamed 2>&1)"; LMV_RC=$?
[ "$LMV_RC" -eq 0 ] || { printf '%s\n' "$LIVEMV_OUT" | tail -30; fail "choudoufu live-mv exited $LMV_RC"; }
grep -qF 'Rewrote the ownership marker on one live resource. This was a cloud write.' <<< "$LIVEMV_OUT" \
  || { printf '%s\n' "$LIVEMV_OUT"; fail "live-mv did not report a real cloud write"; }
grep -qF '"aws_iam_instance_profile.team_0001_profile" -> "aws_iam_instance_profile.team_0001_profile_renamed"' <<< "$LIVEMV_OUT" \
  || { printf '%s\n' "$LIVEMV_OUT"; fail "live-mv did not report rewriting the tofu-address marker from the old address to the new one"; }
P1_AFTER="$(awsl iam get-instance-profile --instance-profile-name "${PREFIX}-team-0001-profile" --query 'InstanceProfile.InstanceProfileId' --output text 2>/dev/null || true)"
[ "$P1_AFTER" = "$P1_ID" ] || fail "the instance profile's live id changed across live-mv ($P1_ID -> $P1_AFTER)"
P1_TAG="$(marker_of_profile "$ENDPOINT" "${PREFIX}-team-0001-profile")"
[ "$P1_TAG" = 'aws_iam_instance_profile.team_0001_profile_renamed' ] \
  || fail "the instance profile carries tofu-address=$P1_TAG after live-mv, not aws_iam_instance_profile.team_0001_profile_renamed"
log "  $P1_ID unchanged, tofu-address now aws_iam_instance_profile.team_0001_profile_renamed - read via the AWS CLI"

log "=== I3. one more plan: both renames are complete and invisible ==="
RENAME_FINAL="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; RN_RC=$?
[ "$RN_RC" -eq 0 ] || { printf '%s\n' "$RENAME_FINAL" | tail -30; fail "the post-rename plan exited $RN_RC"; }
plan_is_noop "$RENAME_FINAL" \
  || { grep -E '^  #' <<< "$RENAME_FINAL" | head -20; fail "the post-rename plan is not empty"; }
log "  No changes."
gauntlet_stage day2_rename pass "moved block: aws_iam_instance_profile.team_0000_profile renamed with zero churn (0 add, 1 change, 0 destroy) and the plan itself showed the tofu-address marker being rewritten in place; live-mv: team_0001_profile renamed with no moved block at all, reported as a real cloud write; both live instance-profile ids unchanged and both markers read back at the NEW address via the AWS CLI; stock's own oracle over the identical two renames on cold_deploy's state (B1) is also zero churn (No changes., both moves reported); the plan after both renames is empty"

# ══════════════════════════════════════════════════════════════════════════
# PART J: REMOVE A BLOCK (day2_remove)
# ══════════════════════════════════════════════════════════════════════════
#
# Two blocks at once, which is what this stage's own text asks for
# ("including blocks for untaggable children whose parents stay"):
# aws_iam_instance_profile.team_0002_profile is taggable and marked, and
# aws_iam_role_policy.team_0002_inline is untaggable - it has no tags
# argument in the provider schema at all, so it carries no marker and its
# identity composes from aws_iam_role.team_0002_role, which stays declared.

gauntlet_begin_stage day2_remove
log "=== J0. capture the two live objects a removal must actually destroy ==="
R2_PROFILE_N="$(awsl iam list-instance-profiles --query "length(InstanceProfiles[?InstanceProfileName=='${PREFIX}-team-0002-profile'])" --output text)"
[ "$R2_PROFILE_N" = "1" ] || fail "${PREFIX}-team-0002-profile is not live before day2_remove starts ($R2_PROFILE_N found)"
R2_INLINE="$(awsl iam list-role-policies --role-name "${PREFIX}-team-0002-role" --query 'PolicyNames' --output text)"
grep -qF "${PREFIX}-team-0002-inline" <<< "$R2_INLINE" \
  || fail "${PREFIX}-team-0002-inline is not an inline policy on ${PREFIX}-team-0002-role before day2_remove starts"
log "  both live: the instance profile and the untaggable inline policy on ${PREFIX}-team-0002-role"

if [ "${BREAK_REMOVE:-}" = "1" ]; then
  log "=== J1 (BREAK_REMOVE=1). keep both blocks; no destroy may be proposed ==="
  BRM_PLAN="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; BRM_RC=$?
  [ "$BRM_RC" -eq 0 ] || { printf '%s\n' "$BRM_PLAN" | tail -30; fail "the BREAK_REMOVE=1 kept-block plan exited $BRM_RC"; }
  grep -qE 'will be destroyed' <<< "$BRM_PLAN" \
    && { grep -E '^  # .+ will be' <<< "$BRM_PLAN"; fail "BREAK_REMOVE=1: a destroy was proposed even though both blocks are still declared - this stage's check is not load-bearing"; }
  plan_is_noop "$BRM_PLAN" \
    || { grep -E '^  #' <<< "$BRM_PLAN"; fail "BREAK_REMOVE=1: the kept-block plan is not empty"; }
  log "  BREAK_REMOVE=1: correctly proposes nothing - the blocks are still declared"
  not_run_rest "BREAK_REMOVE=1 control run: this run exists to prove day2_remove's destroy assertion is load-bearing and stops once it has" \
    day2_remove day2_count day2_replace strict
  gauntlet_end
  exit 0
fi

log "=== J1. choudoufu: delete the instance-profile block and the untaggable inline-policy block ==="
render_config "$ADOPTED" live rename0 moved0 rename1 remove2
REMOVE_PLAN="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; RM_RC=$?
[ "$RM_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_PLAN" | tail -30; fail "the day2_remove plan exited $RM_RC"; }
if grep -q 'is unclaimed, so this may be the same resource under a new instance key' <<< "$REMOVE_PLAN"; then
  printf '%s\n' "$REMOVE_PLAN" | tail -30
  fail "choudoufu withheld a destroy as a possible rename (discovery.go's classifyOrphans) even though no other block of that type and name is declared anywhere in this config"
fi
grep -qE '^  # aws_iam_instance_profile\.team_0002_profile will be destroyed' <<< "$REMOVE_PLAN" \
  || { grep -E '^  # .+ will be' <<< "$REMOVE_PLAN"; fail "choudoufu does not propose destroying aws_iam_instance_profile.team_0002_profile when its block is deleted"; }
grep -qE '^  # aws_iam_role_policy\.team_0002_inline will be destroyed' <<< "$REMOVE_PLAN" \
  || { grep -E '^  # .+ will be' <<< "$REMOVE_PLAN"; fail "choudoufu does not propose destroying the untaggable aws_iam_role_policy.team_0002_inline when its block is deleted"; }
grep -qF 'Plan: 0 to add, 0 to change, 2 to destroy.' <<< "$REMOVE_PLAN" \
  || { printf '%s\n' "$REMOVE_PLAN" | tail -12; fail "choudoufu's remove plan proposes something other than exactly two destroys"; }
log "  choudoufu: exactly two destroys, the same two stock's own oracle (B2) proposed"

REMOVE_APPLY="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RM_RC=$?
[ "$RM_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_APPLY" | tail -30; fail "the day2_remove apply exited $RM_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 2 destroyed' <<< "$REMOVE_APPLY" \
  || { grep -E 'Apply complete' <<< "$REMOVE_APPLY"; fail "the day2_remove apply was not exactly two destroys"; }
# Confirmed by COUNT, never by an exit code: an emulator may answer a
# deleted id with an empty list rather than the NotFound real AWS documents.
R2_PROFILE_AFTER="$(awsl iam list-instance-profiles --query "length(InstanceProfiles[?InstanceProfileName=='${PREFIX}-team-0002-profile'])" --output text)"
[ "$R2_PROFILE_AFTER" = "0" ] || fail "${PREFIX}-team-0002-profile still exists after the destroy ($R2_PROFILE_AFTER found) - it was orphaned, not destroyed"
R2_INLINE_AFTER="$(awsl iam list-role-policies --role-name "${PREFIX}-team-0002-role" --query 'PolicyNames' --output text)"
grep -qF "${PREFIX}-team-0002-inline" <<< "$R2_INLINE_AFTER" \
  && fail "${PREFIX}-team-0002-inline is still an inline policy on ${PREFIX}-team-0002-role after the destroy"
R2_ROLE_N="$(awsl iam list-roles --query "length(Roles[?RoleName=='${PREFIX}-team-0002-role'])" --output text)"
[ "$R2_ROLE_N" = "1" ] || fail "the parent role ${PREFIX}-team-0002-role was destroyed too - only the child's block was removed"
log "  both objects genuinely gone and the parent role still live - all three facts read via the AWS CLI, not through choudoufu's own report"

REMOVE_FINAL="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; RM_RC=$?
[ "$RM_RC" -eq 0 ] || { printf '%s\n' "$REMOVE_FINAL" | tail -30; fail "the post-remove plan exited $RM_RC"; }
plan_is_noop "$REMOVE_FINAL" \
  || { grep -E '^  #' <<< "$REMOVE_FINAL" | head -20; fail "the post-remove plan is not empty"; }
log "  No changes."
gauntlet_stage day2_remove pass "deleting two blocks - the taggable, marked aws_iam_instance_profile.team_0002_profile and the UNTAGGABLE aws_iam_role_policy.team_0002_inline, whose parent role stays declared - proposed exactly two destroys (0 add, 0 change, 2 destroy) in an order the cloud accepted, matching stock's own plan for the same two removals on cold_deploy's state (B2); the apply destroyed exactly two, both objects are confirmed gone and the parent role confirmed still live via the AWS CLI, and the next plan is empty"

# ══════════════════════════════════════════════════════════════════════════
# PART K: CHANGE COUNT (day2_count)
# ══════════════════════════════════════════════════════════════════════════
#
# The estate's own count block, not an added fixture: terralith-gen emits
# six declarations each carrying `count = 2` (aws_iam_role.count_team and
# its inline policy, customer-managed policy, two attachments and instance
# profile). Scaling all six to 1 and back is a twelve-instance cycle over
# four resource types, two of them untaggable.

gauntlet_begin_stage day2_count
log "=== K0. capture the index-[0] identities that must survive the whole cycle ==="
C0_ROLE_ID="$(awsl iam get-role --role-name "${PREFIX}-count-team-0000-role" --query 'Role.RoleId' --output text)"
[ -n "$C0_ROLE_ID" ] && [ "$C0_ROLE_ID" != "None" ] || fail "no live ${PREFIX}-count-team-0000-role before day2_count starts"
C0_ROLE_TAG="$(marker_of_role "$ENDPOINT" "${PREFIX}-count-team-0000-role")"
[ "$C0_ROLE_TAG" = "$(escape_address 'aws_iam_role.count_team[0]')" ] \
  || fail "count_team[0]'s role carries tofu-address=$C0_ROLE_TAG before day2_count, not the escaped form of aws_iam_role.count_team[0]"
C0_PROFILE_TAG="$(marker_of_profile "$ENDPOINT" "${PREFIX}-count-team-0000-profile")"
[ "$C0_PROFILE_TAG" = "$(escape_address 'aws_iam_instance_profile.count_team_profile[0]')" ] \
  || fail "count_team_profile[0] carries tofu-address=$C0_PROFILE_TAG before day2_count, not the escaped form of aws_iam_instance_profile.count_team_profile[0]"
log "  count_team[0] role id $C0_ROLE_ID, markers on the role and the profile confirmed by value"

log "=== K1. choudoufu: scale the six count_team blocks from 2 to 1 ==="
render_config "$ADOPTED" live rename0 moved0 rename1 remove2 count1
DOWN_PLAN="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; CT_RC=$?
[ "$CT_RC" -eq 0 ] || { printf '%s\n' "$DOWN_PLAN" | tail -30; fail "the day2_count scale-down plan exited $CT_RC"; }
DOWN_1_N="$(grep -cE '^  # \S+\[1\] will be destroyed' <<< "$DOWN_PLAN" || true)"
if [ "${BREAK_COUNT:-}" = "1" ]; then
  log "  BREAK_COUNT=1: asserting the WRONG instances (index [0]) were the ones destroyed"
  if grep -qE '^  # \S+\[0\] will be destroyed' <<< "$DOWN_PLAN"; then
    grep -E '^  # .+ will be' <<< "$DOWN_PLAN"
    fail "BREAK_COUNT=1: the plan actually destroys an index-[0] instance - this assertion is not load-bearing"
  fi
  log "  BREAK_COUNT=1: correctly does NOT destroy any index-[0] instance - the wrong-instance assertion fails to hold, as it must"
  not_run_rest "BREAK_COUNT=1 control run: this run exists to prove day2_count's destroyed-instance assertion is load-bearing and stops once it has" \
    day2_count day2_replace strict
  gauntlet_end
  exit 0
fi
grep -qE '^  # \S+\[0\] will be' <<< "$DOWN_PLAN" \
  && { grep -E '^  # .+ will be' <<< "$DOWN_PLAN"; fail "the scale-down plan touches an index-[0] instance, which must be untouched"; }
[ "$DOWN_1_N" = "6" ] \
  || { grep -E '^  # .+ will be' <<< "$DOWN_PLAN"; fail "the scale-down plan destroys $DOWN_1_N index-[1] instances, not 6"; }
grep -qF 'Plan: 0 to add, 0 to change, 6 to destroy.' <<< "$DOWN_PLAN" \
  || { printf '%s\n' "$DOWN_PLAN" | tail -12; fail "the scale-down plan proposes something other than exactly six destroys"; }
log "  choudoufu: exactly six destroys, all index [1] - the same shape stock's oracle (G1) produced"
DOWN_APPLY="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CT_RC=$?
[ "$CT_RC" -eq 0 ] || { printf '%s\n' "$DOWN_APPLY" | tail -30; fail "the day2_count scale-down apply exited $CT_RC"; }
grep -qE 'Resources: 0 added, 0 changed, 6 destroyed' <<< "$DOWN_APPLY" \
  || { grep -E 'Apply complete' <<< "$DOWN_APPLY"; fail "the scale-down apply was not exactly six destroys"; }
C1_ROLE_N="$(awsl iam list-roles --query "length(Roles[?RoleName=='${PREFIX}-count-team-0001-role'])" --output text)"
[ "$C1_ROLE_N" = "0" ] || fail "count_team[1]'s role still exists after the scale-down destroy"
C0_ROLE_AFTER="$(awsl iam get-role --role-name "${PREFIX}-count-team-0000-role" --query 'Role.RoleId' --output text)"
[ "$C0_ROLE_AFTER" = "$C0_ROLE_ID" ] || fail "count_team[0]'s role id changed across the scale-down ($C0_ROLE_ID -> $C0_ROLE_AFTER)"
[ "$(marker_of_role "$ENDPOINT" "${PREFIX}-count-team-0000-role")" = "$(escape_address 'aws_iam_role.count_team[0]')" ] \
  || fail "count_team[0]'s marker no longer reads aws_iam_role.count_team[0] after the scale-down"
log "  index [1] genuinely gone, index [0] keeps both its live id and its identity"

log "=== K2. choudoufu: scale the same six blocks back from 1 to 2 ==="
render_config "$ADOPTED" live rename0 moved0 rename1 remove2
UP_PLAN="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; CT_RC=$?
[ "$CT_RC" -eq 0 ] || { printf '%s\n' "$UP_PLAN" | tail -30; fail "the day2_count scale-up plan exited $CT_RC"; }
grep -qE '^  # \S+\[0\] will be' <<< "$UP_PLAN" \
  && { grep -E '^  # .+ will be' <<< "$UP_PLAN"; fail "the scale-up plan touches an index-[0] instance, which must be untouched"; }
UP_1_N="$(grep -cE '^  # \S+\[1\] will be created' <<< "$UP_PLAN" || true)"
[ "$UP_1_N" = "6" ] \
  || { grep -E '^  # .+ will be' <<< "$UP_PLAN"; fail "the scale-up plan creates $UP_1_N index-[1] instances, not 6"; }
grep -qF 'Plan: 6 to add, 0 to change, 0 to destroy.' <<< "$UP_PLAN" \
  || { printf '%s\n' "$UP_PLAN" | tail -12; fail "the scale-up plan proposes something other than exactly six creates"; }
UP_APPLY="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; CT_RC=$?
[ "$CT_RC" -eq 0 ] || { printf '%s\n' "$UP_APPLY" | tail -30; fail "the day2_count scale-up apply exited $CT_RC"; }
grep -qE 'Resources: 6 added, 0 changed, 0 destroyed' <<< "$UP_APPLY" \
  || { grep -E 'Apply complete' <<< "$UP_APPLY"; fail "the scale-up apply was not exactly six creates"; }
C1_ROLE_BACK="$(awsl iam get-role --role-name "${PREFIX}-count-team-0001-role" --query 'Role.RoleId' --output text)"
[ -n "$C1_ROLE_BACK" ] && [ "$C1_ROLE_BACK" != "None" ] || fail "count_team[1]'s role was not recreated by the scale-up"
[ "$(marker_of_role "$ENDPOINT" "${PREFIX}-count-team-0001-role")" = "$(escape_address 'aws_iam_role.count_team[1]')" ] \
  || fail "the recreated count_team[1] role does not carry tofu-address=aws_iam_role.count_team[1]"
C0_ROLE_FINAL="$(awsl iam get-role --role-name "${PREFIX}-count-team-0000-role" --query 'Role.RoleId' --output text)"
[ "$C0_ROLE_FINAL" = "$C0_ROLE_ID" ] || fail "count_team[0]'s role id changed across the scale-up ($C0_ROLE_ID -> $C0_ROLE_FINAL)"
[ "$(marker_of_profile "$ENDPOINT" "${PREFIX}-count-team-0000-profile")" = "$(escape_address 'aws_iam_instance_profile.count_team_profile[0]')" ] \
  || fail "count_team_profile[0]'s marker did not survive the full count cycle"
COUNT_FINAL="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; CT_RC=$?
[ "$CT_RC" -eq 0 ] || { printf '%s\n' "$COUNT_FINAL" | tail -30; fail "the post-count plan exited $CT_RC"; }
plan_is_noop "$COUNT_FINAL" \
  || { grep -E '^  #' <<< "$COUNT_FINAL" | head -20; fail "the post-count plan is not empty"; }
log "  index [1] recreated and correctly re-marked, index [0] untouched throughout, next plan empty"
gauntlet_stage day2_count pass "the estate's OWN count block - six declarations across four resource types, two of them untaggable - scaled 2 to 1 and back: exactly six index-[1] destroys then exactly six index-[1] creates, no index-[0] instance touched in either plan, matching stock's own applied cycle over the identical six-block shape in a separate account (G1); across the whole cycle count_team[0]'s live role id was unchanged and its marker still reads aws_iam_role.count_team[0], count_team_profile[0]'s still reads aws_iam_instance_profile.count_team_profile[0], the recreated count_team[1] carries aws_iam_role.count_team[1], and the plan afterwards is empty"

# ══════════════════════════════════════════════════════════════════════════
# PART L: REPLACE WITH create_before_destroy (day2_replace)
# ══════════════════════════════════════════════════════════════════════════
#
# aws_iam_instance_profile.team_0004_profile's `name` is ForceNew and
# nothing in the estate references the profile, so changing it is a
# genuinely isolated single-resource replace with no cascade. Under
# create_before_destroy the new profile is created while the old one still
# exists - both pointing at the same role, which floci accepts and which was
# checked directly against the emulator's IAM API with no terraform in the
# loop before this leg was written.

gauntlet_begin_stage day2_replace
log "=== L0. capture the live profile ahead of the forced replace ==="
RP_OLD_ID="$(awsl iam get-instance-profile --instance-profile-name "${PREFIX}-team-0004-profile" --query 'InstanceProfile.InstanceProfileId' --output text)"
[ -n "$RP_OLD_ID" ] && [ "$RP_OLD_ID" != "None" ] || fail "no live ${PREFIX}-team-0004-profile before day2_replace starts"
[ "$(marker_of_profile "$ENDPOINT" "${PREFIX}-team-0004-profile")" = 'aws_iam_instance_profile.team_0004_profile' ] \
  || fail "team_0004_profile does not carry its own tofu-address before the replace"
log "  ${PREFIX}-team-0004-profile=$RP_OLD_ID, marked aws_iam_instance_profile.team_0004_profile"

log "=== L1. choudoufu: change the ForceNew name under create_before_destroy ==="
render_config "$ADOPTED" live rename0 moved0 rename1 remove2 replace4
RP_PLAN="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; RP_RC=$?
[ "$RP_RC" -eq 0 ] || { printf '%s\n' "$RP_PLAN" | tail -40; fail "the day2_replace plan exited $RP_RC"; }
grep -qE '^  # aws_iam_instance_profile\.team_0004_profile must be replaced' <<< "$RP_PLAN" \
  || { grep -E '^  # .+ (will be|must be)' <<< "$RP_PLAN"; fail "choudoufu does not propose replacing aws_iam_instance_profile.team_0004_profile when its ForceNew name argument changes"; }
grep -qF 'Plan: 1 to add, 0 to change, 1 to destroy.' <<< "$RP_PLAN" \
  || { printf '%s\n' "$RP_PLAN" | tail -12; fail "the day2_replace plan is not exactly one isolated replace"; }
log "  choudoufu: exactly one isolated replace at the same declared address - the same shape stock's oracle (B3) produced"
RP_APPLY="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RP_RC=$?
[ "$RP_RC" -eq 0 ] || { printf '%s\n' "$RP_APPLY" | tail -40; fail "the day2_replace apply exited $RP_RC"; }
grep -qE 'Resources: 1 added, 0 changed, 1 destroyed' <<< "$RP_APPLY" \
  || { grep -E 'Apply complete' <<< "$RP_APPLY"; fail "the day2_replace apply was not exactly one create and one destroy"; }
RP_OLD_N="$(awsl iam list-instance-profiles --query "length(InstanceProfiles[?InstanceProfileName=='${PREFIX}-team-0004-profile'])" --output text)"
[ "$RP_OLD_N" = "0" ] || fail "the old ${PREFIX}-team-0004-profile still exists after the replace ($RP_OLD_N found) - the destroy half did not happen"
RP_NEW_ID="$(awsl iam get-instance-profile --instance-profile-name "${PREFIX}-team-0004-profile-replaced" --query 'InstanceProfile.InstanceProfileId' --output text)"
[ -n "$RP_NEW_ID" ] && [ "$RP_NEW_ID" != "None" ] || fail "the replacement ${PREFIX}-team-0004-profile-replaced does not exist after the replace"
[ "$RP_NEW_ID" != "$RP_OLD_ID" ] || fail "the replacement profile came back with the SAME live id the destroyed one had"
[ "$(marker_of_profile "$ENDPOINT" "${PREFIX}-team-0004-profile-replaced")" = 'aws_iam_instance_profile.team_0004_profile' ] \
  || fail "the replacement profile does not carry tofu-address=aws_iam_instance_profile.team_0004_profile"
log "  old $RP_OLD_ID gone, new $RP_NEW_ID carries the same declared address's marker - both read via the AWS CLI"

if [ "${BREAK_REPLACE:-}" = "1" ]; then
  log "=== L2 (BREAK_REPLACE=1). skip the destroy half: re-create the old object carrying the SAME marker ==="
  awsl iam create-instance-profile --instance-profile-name "${PREFIX}-team-0004-profile" >/dev/null \
    || fail "BREAK_REPLACE=1: could not re-create the old instance profile"
  awsl iam tag-instance-profile --instance-profile-name "${PREFIX}-team-0004-profile" \
    --tags "Key=tofu-estate,Value=$ESTATE" "Key=tofu-address,Value=aws_iam_instance_profile.team_0004_profile" >/dev/null \
    || fail "BREAK_REPLACE=1: could not stamp the re-created profile with the same marker"
  BRP_PLAN="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; BRP_RC=$?
  if [ "$BRP_RC" -eq 0 ] && plan_is_noop "$BRP_PLAN"; then
    printf '%s\n' "$BRP_PLAN" | tail -20
    fail "BREAK_REPLACE=1: with two live objects carrying the same tofu-address, the plan proposed nothing - this stage's collision check is not load-bearing"
  fi
  log "  BREAK_REPLACE=1: with the destroy half skipped, the next plan does NOT come back empty (exit $BRP_RC) - it reports the collision:"
  { grep -m5 -E 'collision|more than one|ambiguous|^Error: ' <<< "$BRP_PLAN" || printf '%s\n' "$BRP_PLAN" | tail -12; } | sed 's/^/    | /'
  not_run_rest "BREAK_REPLACE=1 control run: this run exists to prove day2_replace's no-collision assertion is load-bearing and stops once it has" \
    day2_replace strict
  gauntlet_end
  exit 0
fi

RP_FINAL="$(cd "$ADOPTED" && AWS_ENDPOINT_URL="$ENDPOINT" "$TOFU" plan -input=false -no-color 2>&1)"; RP_RC=$?
[ "$RP_RC" -eq 0 ] || { printf '%s\n' "$RP_FINAL" | tail -30; fail "the post-replace plan exited $RP_RC"; }
plan_is_noop "$RP_FINAL" \
  || { grep -E '^  #' <<< "$RP_FINAL" | head -20; fail "the post-replace plan is not empty - a marker collision or a leftover object"; }
log "  No changes, and no marker collision."
gauntlet_stage day2_replace pass "changing aws_iam_instance_profile.team_0004_profile's ForceNew name under create_before_destroy proposed exactly one isolated replace at the same declared address (1 to add, 0 to change, 1 to destroy), matching stock's own plan for the identical change on cold_deploy's state (B3); the apply created the new object and destroyed the old one, the old name no longer resolves and the new one carries the declared address's marker (both read via the AWS CLI), and the next plan is empty with no collision"

# ══════════════════════════════════════════════════════════════════════════
# strict: not exercised by this script
# ══════════════════════════════════════════════════════════════════════════
gauntlet_stage strict not_run "this crossing script does not exercise the strict toggles: strict is Headline:false in tools/gauntlet/stages.go so it moves neither bar, and a toggle-by-toggle refusal fixture is a separate unit from the crossing this script exists to be. live/e2e/reference-ec2-vpc/run.sh's PART G is the pattern for the estate that does carry one"

gauntlet_end

log ""
log "=== PASS ==="
log ""
log "tools/terralith-gen -scale $SCALE generated $EXPECTED resources; stock stood"
log "them up in both accounts and destroyed one back to an enumerated-empty"
log "account; choudoufu adopted the other from stock's own state file, replanned"
log "empty with every rendered identity checked by value, applied that plan as a"
log "genuine no-op, built the same estate greenfield and matched stock's cloud"
log "object by object, then reconverged one out-of-band drift and carried a"
log "rename, a removal, a full count cycle and a create_before_destroy replace -"
log "each against stock's own plan for the identical change."
