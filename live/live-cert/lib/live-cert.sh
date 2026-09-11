#!/usr/bin/env bash
# live/live-cert/lib/live-cert.sh: shared helpers for a live-AWS certification
# script (issue #440). A crossing script under live/e2e/ measures choudoufu
# against stock, both against the pinned emulator, and never creates real
# billable objects; a script under live/live-cert/ does the opposite - it can
# run against a REAL AWS account, so it carries three obligations no
# live/e2e/*/run.sh needs:
#
#   1. AMI RESOLUTION. live/e2e/reference-ec2-vpc/run.sh applies the literal
#      "ami-12345678", which floci accepts unvalidated but real EC2
#      RunInstances rejects (InvalidAMIID.Malformed/NotFound). livecert_ami
#      below resolves a real, region-valid Amazon Linux AMI id through the
#      SSM public parameter AWS documents for exactly this
#      (/aws/service/ami-amazon-linux-latest/...), the SAME call path
#      whether the endpoint is floci or real AWS - floci answers it too (a
#      fake but syntactically valid id), so this is proven against the
#      emulator before it is ever pointed at a real account, not swapped for
#      a different code path per target.
#
#   2. TEARDOWN ON EVERY EXIT PATH. No script anywhere under live/e2e/ has a
#      real destroy step (`grep -rl "tofu destroy\|terraform destroy" live/e2e/*/run.sh`
#      returns nothing); every one relies on `docker rm -f` discarding the
#      EMULATOR's state. Against real AWS there is no container to discard.
#      A caller sources this file, sets a trap on EXIT INT TERM to
#      livecert_teardown (see reference-ec2-vpc.sh for the exact wiring,
#      including how it stays safe to invoke from a live signal handler
#      while a foreground apply is still running), and livecert_teardown:
#        a. runs a real destroy against whatever the current phase's own
#           tofu/terraform binary and working directory are (destroy's own
#           exit code is logged but never trusted alone);
#        b. VERIFIES the account is empty by LISTING - resourcegroupstaggingapi
#           first, then a per-service fallback (livecert_verify_empty below),
#           because #440's own research run found floci's
#           resourcegroupstaggingapi does not index every type this estate
#           creates (an aws_internet_gateway.main didn't appear in a
#           get-resources listing that otherwise correctly found the vpc,
#           subnet and security group created alongside it - confirmed by a
#           timed kill-mid-apply rehearsal during this issue's own build,
#           2026-08-29);
#        c. if anything survives step b, runs livecert_sweep - a raw AWS CLI
#           force-delete by tag, independent of tofu/terraform entirely, so
#           a destroy that only partially worked (or a destroy binary with a
#           real gap) is not the only chance an object gets cleaned up.
#      This is deliberately redundant: HANDOFF.md's safety rule ("never
#      write a wrong marker") has a mirror image on the spend side - never
#      leave a live object running - and a single mechanism trusted alone is
#      exactly the shape that rule warns against.
#
#   3. RETRY-SAFE TAGGING. Every resource this estate creates, in every
#      phase (the plain stock apply AND the post-migrate adopted config),
#      carries tofu-cert-run=$RUN_ID in its own tags{} block - not only the
#      tofu-estate/tofu-address markers choudoufu writes after migrate, which
#      do not exist yet during the stock cold_deploy phase a kill could land
#      in. livecert_verify_empty and livecert_sweep both key off this one
#      tag, so a retry after a partial teardown finds exactly this run's own
#      objects and nothing a concurrent or earlier run left behind.
#
# Sourced by a script under live/live-cert/<estate>.sh; every function below
# expects REGION and RUN_ID as already-set globals, and ENDPOINT (may be
# empty - real AWS, no override) for whichever CLI calls need it.

# livecert_aws runs the AWS CLI against ENDPOINT when set (floci) or against
# real AWS when ENDPOINT is empty - the one place that branch lives, so every
# caller below (and reference-ec2-vpc.sh itself) writes one code path.
livecert_aws() {
  if [ -n "${ENDPOINT:-}" ]; then
    aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"
  else
    aws --region "$REGION" "$@"
  fi
}

# livecert_rgta_count prints how many resources resourcegroupstaggingapi
# get-resources reports for one tag filter, counting across EVERY page.
# NOT `--query 'length(ResourceTagMappingList)' --output text`: the CLI
# applies length() PER RESULT PAGE (get-resources pages at 50 by default),
# so a filter matching more than one page prints one number per page - e.g.
# "50\n50\n4" for 104 matches at scale=50 - and every numeric comparison
# against that string errors, which made a real run wrongly conclude the
# tagging piece was never exercised (issue #1047). This is the exact bug
# terralith-scale.sh's own ssm_prefix_count already avoids for `ssm
# get-parameters-by-path`, and the fix is the same one applied here:
# --output text DOES correctly concatenate a plain (non-length) array query
# across every page, tab- and newline-separated, so querying the ARNs and
# counting lines counts the whole paginated result exactly once.
# Args: <tag-key> <tag-value>
livecert_rgta_count() {
  livecert_aws resourcegroupstaggingapi get-resources \
    --tag-filters "Key=$1,Values=$2" \
    --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null \
    | tr '\t' '\n' | grep -c . || true
}

# livecert_ami prints a real, region-valid Amazon Linux AMI id on stdout via
# the SSM public parameter AWS documents for exactly this purpose. Answered
# by floci too (a fake but syntactically valid id, confirmed empirically
# while building this script, 2026-08-29), so the SAME call resolves the AMI
# whether ENDPOINT points at the emulator or is unset (real AWS) - closing
# #440's first blocker without a per-target literal to keep in sync.
livecert_ami() {
  local param="/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"
  local ami
  ami="$(livecert_aws ssm get-parameter --name "$param" --query 'Parameter.Value' --output text 2>&1)" || {
    printf 'livecert_ami: ssm get-parameter %s failed: %s\n' "$param" "$ami" >&2
    return 1
  }
  case "$ami" in
    ami-*) printf '%s\n' "$ami"; return 0 ;;
    *) printf 'livecert_ami: unexpected value for %s: %s\n' "$param" "$ami" >&2; return 1 ;;
  esac
}

# livecert_verify_empty checks that nothing tagged tofu-cert-run=$RUN_ID
# remains live or billable. It NEVER trusts a destroy command's own exit
# code (the caller's job, not this function's - see teardown in the crossing
# script): this is the independent listing HANDOFF.md's safety rule and
# #440's own brief both ask for.
#
# resourcegroupstaggingapi is read first, but ONLY as a human-readable hint,
# never as the gate: AWS documents that a just-terminated EC2 instance stays
# tag-visible for a while after termination (and floci matches this - a
# terminated instance kept showing up in a get-resources listing for
# several seconds during this script's own build, 2026-08-29, well after
# terminate-instances had genuinely been accepted), so treating ANY
# RGTA hit as "still not empty" would make this function report a real,
# fully-terminated instance as a leak indefinitely. The actual gate below is
# per-service and per-state: an instance only counts as "still there" in
# pending/running/stopping/stopped - never shutting-down or terminated, both
# of which mean the delete already committed and only AWS's own bookkeeping
# is catching up. Every other type this estate creates (subnet, sg, igw,
# vpc) has no such "gone but still tag-visible" state, so those checks stay
# a flat existence test. resourcegroupstaggingapi is also not complete on its
# own even for the types that ARE a flat test:
# #440's own build found it does not index an aws_internet_gateway on floci
# at all - relying on it alone would have reported "empty" while an
# internet gateway was still live. Prints what it found; returns 0 only when
# every per-service check agrees nothing remains.
livecert_verify_empty() {
  local tag="tofu-cert-run"
  local dirty=0

  local rgta_n
  rgta_n="$(livecert_rgta_count "$tag" "$RUN_ID")"
  if [ "${rgta_n:-0}" != "0" ]; then
    printf '  livecert_verify_empty: resourcegroupstaggingapi reports %s resource(s) tagged %s=%s (informational only - a just-terminated instance can linger here; the per-service checks below are what actually gates the verdict)\n' "$rgta_n" "$tag" "$RUN_ID"
    livecert_aws resourcegroupstaggingapi get-resources --tag-filters "Key=$tag,Values=$RUN_ID" \
      --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null | tr '\t' '\n' | sed 's/^/    /'
  fi

  local instances
  instances="$(livecert_aws ec2 describe-instances \
    --filters "Name=tag:$tag,Values=$RUN_ID" "Name=instance-state-name,Values=pending,running,stopping,stopped" \
    --query 'Reservations[].Instances[].InstanceId' --output text 2>/dev/null || true)"
  [ -n "$instances" ] && { printf '  livecert_verify_empty: live instance(s): %s\n' "$instances"; dirty=1; }

  local sgs
  sgs="$(livecert_aws ec2 describe-security-groups --filters "Name=tag:$tag,Values=$RUN_ID" \
    --query 'SecurityGroups[].GroupId' --output text 2>/dev/null || true)"
  [ -n "$sgs" ] && { printf '  livecert_verify_empty: live security group(s): %s\n' "$sgs"; dirty=1; }

  local igws
  igws="$(livecert_aws ec2 describe-internet-gateways --filters "Name=tag:$tag,Values=$RUN_ID" \
    --query 'InternetGateways[].InternetGatewayId' --output text 2>/dev/null || true)"
  [ -n "$igws" ] && { printf '  livecert_verify_empty: live internet gateway(s): %s\n' "$igws"; dirty=1; }

  local subnets
  subnets="$(livecert_aws ec2 describe-subnets --filters "Name=tag:$tag,Values=$RUN_ID" \
    --query 'Subnets[].SubnetId' --output text 2>/dev/null || true)"
  [ -n "$subnets" ] && { printf '  livecert_verify_empty: live subnet(s): %s\n' "$subnets"; dirty=1; }

  local vpcs
  vpcs="$(livecert_aws ec2 describe-vpcs --filters "Name=tag:$tag,Values=$RUN_ID" \
    --query 'Vpcs[].VpcId' --output text 2>/dev/null || true)"
  [ -n "$vpcs" ] && { printf '  livecert_verify_empty: live vpc(s): %s\n' "$vpcs"; dirty=1; }

  [ "$dirty" = "0" ]
}

# livecert_sweep force-deletes everything tagged tofu-cert-run=$RUN_ID
# directly through the AWS CLI, with NO tofu or terraform involved - the
# belt-and-suspenders path #440's brief asks for, reachable even if the
# destroy command a phase would normally use has a real gap, or a retry
# finds a previous run's partial teardown. Deletion order matches AWS's own
# dependency requirements: instances before security groups (an SG in use by
# a running instance cannot be deleted), everything before its subnet,
# subnet and internet gateway before the vpc, internet gateway detached
# before it is deleted. Every step is best-effort (`|| true`): a resource
# already gone is not an error, and one resource failing to delete must not
# stop the sweep from attempting every other one.
livecert_sweep() {
  local tag="tofu-cert-run"
  printf '  livecert_sweep: force-deleting everything tagged %s=%s\n' "$tag" "$RUN_ID"

  local instances
  instances="$(livecert_aws ec2 describe-instances \
    --filters "Name=tag:$tag,Values=$RUN_ID" "Name=instance-state-name,Values=pending,running,stopping,stopped" \
    --query 'Reservations[].Instances[].InstanceId' --output text 2>/dev/null || true)"
  if [ -n "$instances" ]; then
    printf '    terminating instance(s): %s\n' "$instances"
    livecert_aws ec2 terminate-instances --instance-ids $instances >/dev/null 2>&1 || true
    livecert_aws ec2 wait instance-terminated --instance-ids $instances 2>/dev/null || true
  fi

  local sgs
  sgs="$(livecert_aws ec2 describe-security-groups --filters "Name=tag:$tag,Values=$RUN_ID" \
    --query 'SecurityGroups[].GroupId' --output text 2>/dev/null || true)"
  for sg in $sgs; do
    printf '    deleting security group %s\n' "$sg"
    livecert_aws ec2 delete-security-group --group-id "$sg" >/dev/null 2>&1 || true
  done

  local igws
  igws="$(livecert_aws ec2 describe-internet-gateways --filters "Name=tag:$tag,Values=$RUN_ID" \
    --query 'InternetGateways[].[InternetGatewayId,Attachments[0].VpcId]' --output text 2>/dev/null || true)"
  while read -r igw vpc; do
    [ -n "$igw" ] || continue
    if [ -n "$vpc" ] && [ "$vpc" != "None" ]; then
      printf '    detaching internet gateway %s from %s\n' "$igw" "$vpc"
      livecert_aws ec2 detach-internet-gateway --internet-gateway-id "$igw" --vpc-id "$vpc" >/dev/null 2>&1 || true
    fi
    printf '    deleting internet gateway %s\n' "$igw"
    livecert_aws ec2 delete-internet-gateway --internet-gateway-id "$igw" >/dev/null 2>&1 || true
  done <<< "$igws"

  local subnets
  subnets="$(livecert_aws ec2 describe-subnets --filters "Name=tag:$tag,Values=$RUN_ID" \
    --query 'Subnets[].SubnetId' --output text 2>/dev/null || true)"
  for sn in $subnets; do
    printf '    deleting subnet %s\n' "$sn"
    livecert_aws ec2 delete-subnet --subnet-id "$sn" >/dev/null 2>&1 || true
  done

  local vpcs
  vpcs="$(livecert_aws ec2 describe-vpcs --filters "Name=tag:$tag,Values=$RUN_ID" \
    --query 'Vpcs[].VpcId' --output text 2>/dev/null || true)"
  for vpc in $vpcs; do
    printf '    deleting vpc %s\n' "$vpc"
    livecert_aws ec2 delete-vpc --vpc-id "$vpc" >/dev/null 2>&1 || true
  done
}

# ── the maintainer-run-guard ───────────────────────────────────────────────
#
# livecert_require_maintainer_allow refuses a heavy/paid run unless the
# maintainer has enabled it by hand. LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY
# (the case block above, in every caller) is NOT that guard: an agent can set
# its own environment variable, and did, three times, the night of
# 2026-09-11 - three real-AWS certification cycles and two corpus runs went
# out overnight on an inferred authorization. See CLAUDE.md's
# maintainer-run-guard rule for the incident and why agents are forbidden
# from creating the file this function reads.
#
# The file lives OUTSIDE the repository
# (~/.config/choudoufu/allow-heavy-runs) so no clone, checkout, or generated
# tree can ever carry it by accident, and its single line - "until
# <RFC3339 or YYYY-MM-DDTHH:MM>", local time - is an instant, not a
# boolean, so a forgotten enable expires on its own. `just
# allow-heavy-runs 2h` prints the exact command to write it; nothing in
# this repository ever runs that command itself.
#
# This is the shell half of one rule expressed twice - tools/gauntlet's
# CheckMaintainerAllow (maintainerguard.go) is the Go half, read by `gauntlet
# run` and `gauntlet live-cert`. Both refuse with the same message shape:
# the allow file's path, why it did not pass, and the `just
# allow-heavy-runs` recipe.
#
# Skipped entirely in CI (GITHUB_ACTIONS=true): a scheduled or dispatched
# workflow run IS the maintainer's decision, made once when the workflow was
# authored, not something re-derived from a file in $HOME that would not
# even exist on the runner. Prints its refusal on stderr and exits 1;
# callers are bash scripts under `set -e`-adjacent discipline that already
# treat the sibling LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY check the
# same way, so this sits beside it rather than returning a value the caller
# has to remember to check.
#
# teardown-only never needs this file: terralith-scale.sh's own
# `teardown <work dir>` / LIVECERT_TEARDOWN_ONLY dispatch (#1032) does not
# call this function at all, on purpose. That path only destroys resources
# an EARLIER run already created and then verifies the account is empty -
# it creates nothing new and keeps nothing live - so refusing it would do
# the opposite of what this guard exists to prevent: it would leave a held,
# billing estate stranded live rather than torn down. A full run, a held
# run (LIVECERT_HOLD=1), and a resume (LIVECERT_RESUME) all still call this
# function, because each of those creates or keeps real resources.
# LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY is untouched by any of this -
# teardown-only still requires it, same as every other TARGET=aws path.
livecert_require_maintainer_allow() {
  if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
    return 0
  fi
  local allow_file="${HOME}/.config/choudoufu/allow-heavy-runs"
  local reason=""
  if [ ! -f "$allow_file" ]; then
    reason="the allow file does not exist"
  else
    local line rest until_epoch now_epoch
    line="$(head -n1 "$allow_file")"
    case "$line" in
      "until "*) rest="${line#until }" ;;
      *) rest="" ;;
    esac
    rest="$(printf '%s' "$rest" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
    if [ -z "$rest" ]; then
      reason="its first line is not \"until <timestamp>\" (got: \"$line\")"
    else
      until_epoch="$(livecert_parse_local_timestamp "$rest")" || until_epoch=""
      if [ -z "$until_epoch" ]; then
        reason="its timestamp \"$rest\" could not be parsed as RFC3339 or YYYY-MM-DDTHH:MM"
      else
        now_epoch="$(date +%s)"
        if [ "$now_epoch" -ge "$until_epoch" ]; then
          reason="it expired at $rest"
        fi
      fi
    fi
  fi
  if [ -n "$reason" ]; then
    echo "refusing: $allow_file - $reason; run \`just allow-heavy-runs 2h\` for the exact command to paste - nothing has been started" >&2
    exit 2
  fi
}

# livecert_parse_local_timestamp converts an "until" value - a bare local
# YYYY-MM-DDTHH:MM[:SS], or a full RFC3339 instant with a zone offset or a
# trailing Z - to epoch seconds on stdout, trying GNU date's -d first
# (Linux) and falling back to BSD date's -j/-f (macOS, this repo's own
# development machine): the two platforms this guard actually has to run on
# (CI sets GITHUB_ACTIONS=true and never reaches this function at all - see
# the check at the top of livecert_require_maintainer_allow above). Prints
# nothing and returns nonzero on a value neither can parse.
livecert_parse_local_timestamp() {
  local ts="$1" norm epoch
  # GNU date -d accepts both forms directly. On BSD date this option means
  # something else entirely (or errors outright), so a bogus result here is
  # exactly as likely as a real one; -u keeps a Z-suffixed value's own
  # embedded offset from being reinterpreted through whatever TZ the shell
  # happens to have, without affecting a bare local value tried below.
  epoch="$(TZ="${TZ:-}" date -d "$ts" +%s 2>/dev/null)" && { printf '%s\n' "$epoch"; return 0; }
  # BSD date -j -f needs an exact format with no offset punctuation of its
  # own; normalize "Z" to "+0000" and strip a colon from a "+07:00"-style
  # offset before trying each fixed layout in turn. A bare
  # YYYY-MM-DDTHH:MM with no seconds field gets ":00" appended before
  # parsing, never parsed via the seconds-less "%Y-%m-%dT%H:%M" layout
  # directly - BSD's strptime leaves a format's omitted fields (seconds,
  # here) filled from the CURRENT wall-clock time rather than zeroed, so
  # parsing "2026-09-12T08:00" without this fixup silently produced a
  # result off by however many seconds past the minute this function
  # happened to run at (caught testing this guard by hand: two calls a
  # second apart returned epochs 7 seconds apart for the identical input).
  case "$ts" in
    *Z) norm="${ts%Z}+0000" ;;
    *) norm="$ts" ;;
  esac
  norm="$(printf '%s' "$norm" | sed -E 's/([+-][0-9]{2}):([0-9]{2})$/\1\2/')"
  case "$norm" in
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]) norm="${norm}:00" ;;
  esac
  for fmt in "%Y-%m-%dT%H:%M:%S%z" "%Y-%m-%dT%H:%M:%S"; do
    epoch="$(date -j -f "$fmt" "$norm" +%s 2>/dev/null)" && { printf '%s\n' "$epoch"; return 0; }
  done
  return 1
}
