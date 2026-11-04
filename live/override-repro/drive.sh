#!/bin/bash
# drive.sh TARGET REPRO - the "before" (and, with overrides on, "after") half
# of a provider override's proof (GitHub issue #2309).
#
#   TARGET   floci (default endpoint http://localhost:4566, override with
#            FLOCI_ENDPOINT) or aws (real AWS, us-east-2, whatever
#            credentials are in the environment).
#   REPRO    one directory here: v1/ is applied, v2/ (if present) is the
#            mutation, kind says what to do with it.
#
# A real-AWS run spends money. It is the maintainer's call, per run.
#
# Logs and the verdict go to $WORK/TARGET/REPRO (WORK defaults to a temp
# dir). Read the verdict and the log, not this script's exit status.
set -u
T=$1; X=$2; R=$(cd "$(dirname "$0")" && pwd)
B=${CHOUDOUFU_BIN:-choudoufu}
WORK=${WORK:-$(mktemp -d)}
W=$WORK/$T/$X; rm -rf "$W"; mkdir -p "$W"; cp "$R/_common/versions.tf" "$R/$X/v1/"* "$W/"
K=$(cat "$R/$X/kind"); L=$W/log; : > "$L"
export TF_IN_AUTOMATION=1 TF_INPUT=0
if [ "$T" = floci ]; then
  # localhost, not 127.0.0.1: S3 Control prefixes the account ID onto the
  # endpoint host (internal/live/flocitest/proxy.go).
  export AWS_ENDPOINT_URL=${FLOCI_ENDPOINT:-http://localhost:4566} AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
  unset AWS_PROFILE
fi
step(){ echo "### $*" >>"$L"; (cd "$W" && "$B" "$@" -no-color >>"$L" 2>&1); rc=$?; echo "### rc=$rc" >>"$L"; return $rc; }
v(){ echo "$*" >>"$W/verdict"; }
step init >/dev/null || { v "init failed"; echo "$W"; exit 1; }
step apply -auto-approve; a=$?
v "apply v1 rc=$a"
if [ $a -eq 0 ]; then
  case $K in
    plan-after-apply) step plan -detailed-exitcode; v "plan after apply rc=$? (2 = a diff)";;
    mutate-plan) cp "$R/$X/v2/"* "$W/"; step plan -detailed-exitcode; v "plan after mutation rc=$? (2 = a diff)";;
    mutate-apply) step plan -detailed-exitcode; v "plan after apply rc=$?"; cp "$R/$X/v2/"* "$W/"; step plan -detailed-exitcode; v "plan v2 rc=$?"; step apply -auto-approve; v "apply v2 rc=$?";;
    mutate-destroy) cp "$R/$X/v2/"* "$W/"; step destroy -auto-approve; v "destroy with v2 config rc=$?"; step apply -auto-approve; v "apply v2 rc=$?";;
    apply-only) :;;
  esac
fi
step destroy -auto-approve; d=$?; v "destroy rc=$d"
if [ $d -ne 0 ]; then step destroy -auto-approve; v "destroy retry rc=$?"; fi
(cd "$W" && "$B" state list 2>/dev/null) > "$W/leftover"; v "left in state: $(wc -l < "$W/leftover" | tr -d ' ')"
echo "$W"
