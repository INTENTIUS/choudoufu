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
# remains live. It NEVER trusts a destroy command's own exit code (the
# caller's job, not this function's - see livecert_teardown in the crossing
# script): this is the independent listing HANDOFF.md's safety rule and
# #440's own brief both ask for. resourcegroupstaggingapi is tried first
# (matches the pattern live/e2e/reference-ec2-vpc/run.sh's B5 test_apply
# stage already uses), then every EC2 resource type this estate can create
# is checked directly by tag, because #440's own build found
# resourcegroupstaggingapi does not index an aws_internet_gateway on floci -
# relying on it alone would have reported "empty" while an internet gateway
# was still live. Prints what it found; returns 0 only when every check
# agrees nothing remains.
livecert_verify_empty() {
  local tag="tofu-cert-run"
  local dirty=0

  local rgta_n
  rgta_n="$(livecert_aws resourcegroupstaggingapi get-resources \
    --tag-filters "Key=$tag,Values=$RUN_ID" \
    --query 'length(ResourceTagMappingList)' --output text 2>/dev/null || echo unknown)"
  if [ "$rgta_n" != "0" ]; then
    printf '  livecert_verify_empty: resourcegroupstaggingapi reports %s resource(s) tagged %s=%s\n' "$rgta_n" "$tag" "$RUN_ID"
    livecert_aws resourcegroupstaggingapi get-resources --tag-filters "Key=$tag,Values=$RUN_ID" \
      --query 'ResourceTagMappingList[].ResourceARN' --output text 2>/dev/null | tr '\t' '\n' | sed 's/^/    /'
    dirty=1
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
