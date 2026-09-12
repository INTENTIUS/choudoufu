#!/usr/bin/env bash
# live/e2e/lib/gauntlet.sh: the stage protocol a crossing script speaks so
# tools/gauntlet can record its verdicts. Source it, call gauntlet_begin
# once, then gauntlet_stage <id> <pass|fail|not_run> [detail] per stage, and
# gauntlet_end last. Every line it prints starts with "GAUNTLET " on stdout
# and is greppable by a human; everything else the script prints is ignored
# by the runner. live/GAUNTLET.md documents the grammar; the stage ids are
# tools/gauntlet/stages.go's.
#
# A script that sources this library but never calls gauntlet_begin is not
# speaking the protocol, and the runner treats it as legacy.

gauntlet_begin() {
  # _GAUNTLET_LAST_T anchors the first stage's duration_s: the wall-clock
  # elapsed since gauntlet_begin ran, not since some other clock. Every
  # crossing script already does its real work strictly between one
  # gauntlet_stage call and the next (issue #434 verified this against every
  # current script before relying on it), so the delta between consecutive
  # GAUNTLET timestamps is that stage's wall-clock time - computed here in
  # the shell, once per call, rather than as a raw timestamp the Go side
  # would have to subtract, so a script's own stdout stays self-describing.
  _GAUNTLET_LAST_T=$(date +%s)
  CURRENT_STAGE=""
  printf 'GAUNTLET protocol=1\n'
}

# gauntlet_begin_stage <id>: marks the start of stage <id>'s own work, for a
# script's fail() to blame if something goes wrong before the stage reports
# its own verdict. Sets the CURRENT_STAGE variable every crossing script's
# own fail() already reads (`if [ -n "$CURRENT_STAGE" ]; then gauntlet_stage
# "$CURRENT_STAGE" fail "$*"; fi`) - no new mechanism, just a name for the
# assignment every script was already spelling out by hand.
#
# Pair every gauntlet_begin_stage with either a gauntlet_stage call for that
# same id (which clears CURRENT_STAGE itself, see below) or an explicit
# gauntlet_end_stage, so the setup for whatever comes NEXT never runs while
# CURRENT_STAGE still names a stage that has already finished (issue #555:
# a script that assigns CURRENT_STAGE=X once and only reassigns it when
# stage Y begins leaves every line in between - including setup work for Y
# that has nothing to do with X - attributed to X if it fails).
gauntlet_begin_stage() {
  CURRENT_STAGE="$1"
}

# gauntlet_end_stage: clears CURRENT_STAGE, so a failure from this point on -
# setup for the next stage, teardown, an oracle comparison that never itself
# reports a verdict - has no stage to blame. fail()'s own guard
# (`if [ -n "$CURRENT_STAGE" ]`) already treats an empty CURRENT_STAGE as
# "no verdict to record," so this is enough to make an unattributed window
# genuinely unattributed instead of silently inheriting whatever stage ran
# last.
#
# gauntlet_stage (below) already calls this once a verdict is actually
# recorded, so most scripts never need to call it directly - it exists for a
# block of stage-labelled work that does NOT end in its own gauntlet_stage
# call, e.g. an oracle computation run under `gauntlet_begin_stage day2_X`
# well before day2_X's own real verdict is reported much later in the
# script (corpus-hongbomiao-labelbox and corpus-iam-policy's own STAGE
# 1.5/1.5.5/1.5.6 oracle blocks are exactly this shape).
gauntlet_end_stage() {
  CURRENT_STAGE=""
}

# gauntlet_stage <id> <verdict> [detail...]
# The detail may contain spaces and '=' freely; it runs to end of line, so
# duration_s is emitted before it. Newlines in the detail are replaced by
# spaces so the line stays one line.
#
# Clears CURRENT_STAGE before returning (see gauntlet_end_stage): once a
# stage's verdict has been recorded, anything that runs afterwards - even in
# the same script, even one line later - is by definition no longer that
# stage's own work, so a failure there must never be attributed to a stage
# that already reported its verdict (issue #555).
gauntlet_stage() {
  local id="$1" verdict="$2"
  shift 2 || true
  case "$verdict" in
    pass|fail|not_run) ;;
    *) printf 'gauntlet_stage: verdict %q for stage %s must be pass, fail or not_run\n' "$verdict" "$id" >&2; exit 2 ;;
  esac
  local now dur
  now=$(date +%s)
  dur=$(( now - ${_GAUNTLET_LAST_T:-$now} ))
  _GAUNTLET_LAST_T=$now
  if [ "$#" -gt 0 ]; then
    local detail
    detail="$(printf '%s' "$*" | tr '\n\r' '  ')"
    printf 'GAUNTLET stage=%s verdict=%s duration_s=%s detail=%s\n' "$id" "$verdict" "$dur" "$detail"
  else
    printf 'GAUNTLET stage=%s verdict=%s duration_s=%s\n' "$id" "$verdict" "$dur"
  fi
  gauntlet_end_stage
}

gauntlet_end() {
  printf 'GAUNTLET end=1\n'
}

# gauntlet_pin_aws_provider <versions.tf path>: rewrites a freshly copied
# corpus module's `hashicorp/aws` requirement to an exact version, read from
# live/oracle-versions.json's aws_provider_version field (issue #1034).
#
# A plain `terraform init` resolves a corpus module's bare `>= X` lower
# bound against registry.terraform.io's newest matching release. choudoufu's
# own init resolves the identical bare `hashicorp/aws` source against
# registry.opentofu.org, because this fork is an OpenTofu fork - an
# independent mirror that can lag registry.terraform.io by hours. On
# 2026-09-09 that lag put hashicorp/aws 6.64.0 on the former and not yet on
# the latter, so stock's cold_deploy and choudoufu's later stages read two
# different provider versions off the SAME .terraform.lock.hcl. Pinning both
# sides to one exact release - checked to exist on both registries before a
# human bumps it, the same way live/floci-image and
# live/oracle-versions.json's own terraform_version/tofu_version are bumped
# deliberately rather than left to float - makes which registry answered
# irrelevant.
#
# Call this once per corpus module copy, right after the copy and before
# its first `terraform init`/`tofu init`, on the ROOT (example) directory's
# own versions.tf - the one terraform actually inits in. A child module's
# own required_providers (if any) is left untouched: every corpus module
# used here declares only a lower bound there (">= 6.28" and similar), which
# an exact pin at the root satisfies by intersection, so the child's
# constraint never needs rewriting. A later `cp -R` of an already-pinned
# tree (greenfield, an oracle copy taken from $EST rather than from the
# pristine corpus source) inherits the pin for free and needs no second
# call - see each crossing script's own comment at its cp sites for which
# case it is.
#
# live/pins_drift_test.go's TestGauntletCrossingScriptsPinOneAWSProvider
# checks every one of #1034's five estates calls this and none passes
# terraform/tofu init an -upgrade flag, which would re-float the version
# this function just pinned.
gauntlet_pin_aws_provider() {
  local target="$1" pin
  : "${ROOT:?gauntlet_pin_aws_provider needs $ROOT set (every crossing script sets it before sourcing this file)}"
  pin="$(python3 -c "import json;print(json.load(open('$ROOT/live/oracle-versions.json'))['aws_provider_version'])" 2>/dev/null)"
  [ -n "$pin" ] || { printf 'gauntlet_pin_aws_provider: could not read aws_provider_version from %s/live/oracle-versions.json\n' "$ROOT" >&2; return 1; }
  [ -f "$target" ] || { printf 'gauntlet_pin_aws_provider: %s does not exist\n' "$target" >&2; return 1; }
  AWS_PIN="$pin" perl -0777 -pi -e '
    s/(aws\s*=\s*\{\s*\n\s*source\s*=\s*"hashicorp\/aws"\s*\n\s*version\s*=\s*")[^"]*(")/$1 . "= $ENV{AWS_PIN}" . $2/e;
  ' "$target"
  grep -q "version *= *\"= $pin\"" "$target" \
    || { printf 'gauntlet_pin_aws_provider: %s does not carry the pinned hashicorp/aws version %s after rewrite - the corpus module shape may have moved\n' "$target" "$pin" >&2; return 1; }
}

# gauntlet_kind_up <name> <kubeconfig>: the kind substrate (#1067). A
# kubernetes-lane crossing script runs against a kind cluster instead of a
# floci emulator: a real API server, so what the script asserts is what any
# cluster answers. One cluster per call, named by the caller (name it after
# the estate and the process id, so two runs never collide), its kubeconfig
# written to <kubeconfig> and never to the user's own; the caller exports
# KUBECONFIG (what kubectl reads) and KUBE_CONFIG_PATH (what the
# hashicorp/kubernetes provider and choudoufu's estate sweep read) itself,
# per cluster, since a script may hold two - one the estate lives on, one
# stock's oracle runs on. Needs kind (https://kind.sigs.k8s.io) and kubectl
# on PATH; prints the reason and returns 1 when either is missing, so the
# script's own fail() records the stage it was setting up. The create is
# logged beside the kubeconfig.
gauntlet_kind_up() {
  local name="$1" cfg="$2"
  command -v kind >/dev/null 2>&1 || { printf 'gauntlet_kind_up: kind is not installed; this estate needs a kind cluster (brew install kind)\n' >&2; return 1; }
  command -v kubectl >/dev/null 2>&1 || { printf 'gauntlet_kind_up: kubectl is not installed\n' >&2; return 1; }
  kind create cluster --name "$name" --kubeconfig "$cfg" --wait 120s >"${cfg}.kind.log" 2>&1 \
    || { printf 'gauntlet_kind_up: kind create cluster %s failed:\n' "$name" >&2; tail -5 "${cfg}.kind.log" >&2; return 1; }
}

# gauntlet_kind_down <name>: deletes the cluster gauntlet_kind_up made.
# Idempotent and quiet, so a trap can call it for a cluster that never
# came up.
gauntlet_kind_down() {
  [ -n "${1:-}" ] || return 0
  kind delete cluster --name "$1" >/dev/null 2>&1 || true
}

# gauntlet_kind_count <estate> <kind>...: how many objects of each named
# kind carry tofu-estate=<estate>, summed across every namespace - the
# kind substrate's answer to the tagging index's "objects carrying the
# marker" count that test_apply and day2_teardown compare before and after.
# Reads the cluster KUBECONFIG names.
gauntlet_kind_count() {
  local estate="$1" n=0 k; shift
  for k in "$@"; do
    n=$((n + $(kubectl get "$k" -A -l "tofu-estate=$estate" -o name 2>/dev/null | wc -l | tr -d ' ')))
  done
  printf '%s\n' "$n"
}

# gauntlet_record_count <dir>: counts a record store's on-disk record files
# under <dir> the way every crossing script already counted them by hand -
# "-type f", skipping the write-lock and in-progress-write files a
# staterecord.Store leaves beside its records - PLUS one more exclusion:
# the store's own provisioning sentinel, a single file every store now
# writes once at the top of its own key namespace and never removes
# (internal/live/projection/store.go's provisionStoreSentinel, issue #693).
# The sentinel's on-disk name is ".store-sentinel", mirroring the unexported
# sentinelKeyName constant in that file - a script cannot import a Go
# constant, so this is the one place that spelling is allowed to be
# hand-written a second time; if store.go ever renames it, grep for
# sentinelKeyName and update the literal here. Before this exclusion
# existed, a store touched during a run counted one file too many
# (issue #861).
gauntlet_record_count() {
  find "$1" -type f ! -name '*.lock' ! -name '*.tmp-*' ! -name '.store-sentinel' 2>/dev/null | wc -l | tr -d ' '
}

# gauntlet_stage_from_exit <id> <exit-code> [detail...]
# Convenience for the common shape "run a check, report pass on 0, fail
# otherwise" without the script having to branch itself.
gauntlet_stage_from_exit() {
  local id="$1" code="$2"
  shift 2 || true
  if [ "$code" -eq 0 ]; then
    gauntlet_stage "$id" pass "$@"
  else
    gauntlet_stage "$id" fail "$@"
  fi
}
