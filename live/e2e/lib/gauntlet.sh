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

# gauntlet_refused <scale> <needed> <limit> <unit> <reason...>
#
# The run declines the rung: not a stage that failed, a rung that will not be
# attempted, with the arithmetic that says why (issue #1151). Any of the
# first four arguments may be "-" for "this refusal has no such number";
# needed and limit go together, so give both or neither.
#
#   gauntlet_refused 136 10070 10000 ssm-parameters \
#     "10,069 resources need 10,070 SSM parameters against SSM's hard 10,000 cap (#1146); the uncapped s3 store is blocked behind #1145"
#   gauntlet_refused - - - - "the account's AMI for this region is gone"
#
# Why this is not just `gauntlet_stage <id> not_run "..."`: a stage's not_run
# says one stage did not happen and claims nothing about the run. This says
# the RUN declined, which is what tools/gauntlet needs to know before it
# decides what to write - a refusal is recorded as its own outcome in
# live/gauntlet-scale.json, keyed by scale, and is deliberately NOT written
# to live/gauntlet.json's live_cert row, because that row holds one
# certification per estate and a refusal at one scale must never replace a
# certification at another. Scale 50 cost hours of paid real-AWS runtime;
# scale 136's refusal must land beside it, not on top of it.
#
# The scale matters for exactly that reason: a refusal usually happens before
# cold_deploy, so there is no stage detail to read a scale off, and a refusal
# that cannot name its scale cannot be placed on the ladder at all. Pass it.
#
# Emitting this does not end the run - the caller decides what to do next
# (usually: tear down whatever exists, then exit non-zero).
#
# For live-cert runs today. `gauntlet run` parses the line but has nowhere to
# put it - an estate row is keyed by estate, not by scale, and has no outcome
# field - so it prints that it could not record the refusal rather than
# filing the run as an ordinary one.
gauntlet_refused() {
  local scale="${1:--}" needed="${2:--}" limit="${3:--}" unit="${4:--}"
  shift 4 || true
  local reason
  reason="$(printf '%s' "$*" | tr '\n\r' '  ')"
  if [ -z "$reason" ]; then
    printf 'gauntlet_refused: a refusal with no reason is worth nothing on the record - give one\n' >&2
    exit 2
  fi
  if { [ "$needed" = "-" ] && [ "$limit" != "-" ]; } || { [ "$needed" != "-" ] && [ "$limit" = "-" ]; }; then
    printf 'gauntlet_refused: needed=%s limit=%s - give both or neither; one side of an arithmetic is not an arithmetic\n' "$needed" "$limit" >&2
    exit 2
  fi
  if [ "$needed" != "-" ] && [ "$unit" = "-" ]; then
    printf 'gauntlet_refused: needed=%s limit=%s with no unit - two bare numbers say nothing a later reader can check\n' "$needed" "$limit" >&2
    exit 2
  fi
  local line='GAUNTLET refused=1'
  if [ "$scale" != "-" ]; then line="$line scale=$scale"; fi
  if [ "$needed" != "-" ]; then line="$line needed=$needed limit=$limit"; fi
  if [ "$unit" != "-" ]; then line="$line unit=$unit"; fi
  printf '%s detail=%s\n' "$line" "$reason"
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
# own versions.tf (or main.tf, for the pre-0.13 shape below) - the file
# terraform actually inits against. A child module's own required_providers
# (if any) is left untouched: every corpus module used here declares only a
# lower bound there (">= 6.28" and similar), which an exact pin at the root
# satisfies by intersection, so the child's constraint never needs
# rewriting. A later `cp -R` of an already-pinned tree (greenfield, an
# oracle copy taken from $EST rather than from the pristine corpus source)
# inherits the pin for free and needs no second call - see each crossing
# script's own comment at its cp sites for which case it is.
#
# Two shapes, tried in order (issue #1041, corpus-eks-basic): most corpus
# modules use the modern `required_providers { aws = { source =
# "hashicorp/aws", version = "..." } }` block. .corpus/eks predates
# Terraform 0.13's source addressing entirely - its required_providers is
# the flat `aws = ">= X"` map form, and its own example carries the
# constraint as a deprecated `version` attribute directly on the `provider
# "aws" { ... }` configuration block, with no `source` anywhere to match
# on. Terraform still resolves the bare local name "aws" to
# registry.<host>/hashicorp/aws by default, so the same two-registry lag
# applies; the modern regex simply cannot match this file, so a second
# regex targets the provider-block shape. Whichever one actually matched
# is what the verification below checks for.
#
# live/pins_drift_test.go's TestGauntletCrossingScriptsPinOneAWSProvider
# checks every crossing script that declares hashicorp/aws calls this and
# none passes terraform/tofu init an -upgrade flag, which would re-float
# the version this function just pinned (issue #1041 widened the original
# #1034 five to every such script).
#
# gauntlet_aws_pin_version: the one place a script reads the pin's VALUE
# (rather than applying it to a file) - for a script that needs to build a
# second regex anchored on "the aws provider's current version" further
# down its own pipeline (inserting a live block after required_providers,
# re-verifying a re-copied tree, and similar). Prefer matching the version
# field's *shape* (any quoted string) over calling this and hardcoding the
# returned value into a second literal - a script that never spells the
# pin's digits out a second time cannot drift from the one it already
# applied.
gauntlet_aws_pin_version() {
  : "${ROOT:?gauntlet_aws_pin_version needs $ROOT set (every crossing script sets it before sourcing this file)}"
  python3 -c "import json;print(json.load(open('$ROOT/live/oracle-versions.json'))['aws_provider_version'])" 2>/dev/null
}

gauntlet_pin_aws_provider() {
  local target="$1" pin
  pin="$(gauntlet_aws_pin_version)"
  [ -n "$pin" ] || { printf 'gauntlet_pin_aws_provider: could not read aws_provider_version from %s/live/oracle-versions.json\n' "$ROOT" >&2; return 1; }
  [ -f "$target" ] || { printf 'gauntlet_pin_aws_provider: %s does not exist\n' "$target" >&2; return 1; }
  AWS_PIN="$pin" perl -0777 -pi -e '
    s/(aws\s*=\s*\{\s*\n\s*source\s*=\s*"hashicorp\/aws"\s*\n\s*version\s*=\s*")[^"]*(")/$1 . "= $ENV{AWS_PIN}" . $2/e;
  ' "$target"
  grep -q "version *= *\"= $pin\"" "$target" && return 0

  # The modern shape did not match - try the pre-0.13 provider-block shape.
  AWS_PIN="$pin" perl -0777 -pi -e '
    s/(provider\s+"aws"\s*\{\s*\n\s*version\s*=\s*")[^"]*(")/$1 . "= $ENV{AWS_PIN}" . $2/e;
  ' "$target"
  grep -q "version *= *\"= $pin\"" "$target" \
    || { printf 'gauntlet_pin_aws_provider: %s does not carry the pinned hashicorp/aws version %s after rewrite - the corpus module shape may have moved\n' "$target" "$pin" >&2; return 1; }
}

# gauntlet_aws_required_provider [indent]: prints the hashicorp/aws entry of
# a required_providers block, pinned to the same
# live/oracle-versions.json aws_provider_version gauntlet_pin_aws_provider
# rewrites a copied module to (issue #1216).
#
#     aws = {
#       source  = "hashicorp/aws"
#       version = "= <the pin>"
#     }
#
# gauntlet_pin_aws_provider REWRITES a file somebody else wrote - the right
# shape for a corpus module copied out of .corpus, whose own constraint
# arrives as a bare lower bound. A reference estate has no such file: it
# hand-authors its own root from a heredoc, so there is nothing to rewrite
# and the requirement is text the script itself chooses. Before this
# function that text was the release spelled out by hand, which is how
# reference-ec2-vpc came to measure at 6.58.0 - nineteen copies of one
# version string, written the day the script was - for the five weeks the
# pin moved 6.59.0 and then 6.63.0 underneath it, while every corpus-copying
# estate on the board moved with it.
#
# Read it ONCE into a variable at the top of the script and interpolate that
# variable into each heredoc, rather than calling it per heredoc:
#
#     AWS_REQUIRED_PROVIDER="$(gauntlet_aws_required_provider)" \
#       || fail "could not read the hashicorp/aws pin"
#
# A call whose output is dropped into a heredoc directly cannot be checked -
# an unreadable pin would emit nothing, the terraform block would carry no
# aws requirement at all, and init would quietly resolve the provider from
# the registry, which is the float the pin exists to stop. Assigning it once
# puts the whole script behind one `|| fail`.
#
# indent is the leading whitespace of the entry's own first line (default
# four spaces, the depth of an entry inside `terraform { required_providers
# { ... } }`); the inner lines are indented two further.
#
# live/pins_drift_test.go's TestGauntletCrossingScriptsCarryNoVersionLiteral
# (widened by #1216) checks that no registered crossing script declaring
# hashicorp/aws spells an exact provider version out itself, which is what
# makes this the only way a hand-authored root gets one.
gauntlet_aws_required_provider() {
  local indent="${1:-    }" pin
  pin="$(gauntlet_aws_pin_version)"
  [ -n "$pin" ] || { printf 'gauntlet_aws_required_provider: could not read aws_provider_version from %s/live/oracle-versions.json\n' "$ROOT" >&2; return 1; }
  printf '%saws = {\n%s  source  = "hashicorp/aws"\n%s  version = "= %s"\n%s}\n' "$indent" "$indent" "$indent" "$pin" "$indent"
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

# gauntlet_record_file <dir> <address>: the path of the one record file
# under <dir> whose envelope is for <address>, or nothing if there is none.
# A record's on-disk name is the base64 of its address under a per-type
# directory, which a script has no business reconstructing; the envelope's
# own `address` field is the thing to match, so this reads it. Nothing is
# printed and the status is 1 when no record exists - which is itself a
# reading, not an error: eight of ten Kubernetes instance types get a
# record file and kubernetes_config_map_v1 gets none (#1188).
gauntlet_record_file() {
  python3 - "$1" "$2" <<'PY'
import json, os, sys
root, addr = sys.argv[1], sys.argv[2]
for dirpath, _, names in os.walk(root):
    for n in sorted(names):
        if n.endswith('.lock') or '.tmp-' in n or n == '.store-sentinel':
            continue
        p = os.path.join(dirpath, n)
        try:
            with open(p) as fh:
                d = json.load(fh)
        except Exception:
            continue
        if isinstance(d, dict) and d.get('address') == addr:
            print(p)
            sys.exit(0)
sys.exit(1)
PY
}

# gauntlet_record_residue <file>: the names of the residue attributes a
# record envelope carries, one per line, or nothing. Residue is the
# irrecoverable member - the applied value of a config-only argument the
# API server never returns - so "which names are in here" is what a caller
# wants to assert by value rather than "how many files exist".
gauntlet_record_residue() {
  python3 - "$1" <<'PY'
import json, sys
with open(sys.argv[1]) as fh:
    d = json.load(fh)
for k in sorted((d.get('residue') or {}).get('attributes') or {}):
    print(k)
PY
}

# gauntlet_tagged_count <aws-invocation...>: runs the given AWS CLI
# invocation (e.g. `awsl resourcegroupstaggingapi get-resources
# --tag-filters "Key=tofu-estate,Values=$ESTATE"` - any prefix that ends in
# a `resourcegroupstaggingapi get-resources` call) and prints the true count
# of ResourceTagMappingList entries, summed across every page. The naive
# `--query 'length(ResourceTagMappingList)' --output text` idiom every
# crossing script used to write is wrong past one page: the AWS CLI applies
# --query to EACH page before merging, so past the Tagging API's page size
# of 100 it prints one number per page (e.g. "100 100 100 35") rather than
# the total (issue #1042). Dropping --query lets the CLI's normal automatic
# pagination merge every page's ResourceTagMappingList into one JSON array
# first, so jq's length here is the real total no matter how many pages it
# took. Never add --query back to this call.
gauntlet_tagged_count() {
  "$@" --output json | jq '.ResourceTagMappingList | length'
}

# gauntlet_first_match <jq-selector> <aws-invocation...>: runs the given AWS
# CLI invocation with its pages MERGED and prints the first value the jq
# selector yields - or nothing at all if it yields none.
#
# This is the other half of gauntlet_tagged_count's argument (issue #1214).
# gauntlet_tagged_count exists because summing across pages is easy to get
# wrong; picking the first match across pages is wrong in the same way and
# harder to see, because it prints something plausible rather than an
# obviously silly number. Issue #1206 is the case:
#
#   aws iam list-policies --path-prefix / \
#     --query "Policies[?starts_with(PolicyName, 'x') == \`true\`].Arn | [0]" \
#     --output text
#
# The AWS CLI applies --query to EACH page before merging, so on a listing
# that took 16 pages that command printed SIXTEEN lines: the arn from the
# page holding the match, and the literal "None" from the fifteen that did
# not. The caller captured all sixteen as "the arn", the usual
# `[ -n "$X" ] && [ "$X" != "None" ]` guard passed (a 16-line string is
# neither), and `iam list-policy-tags --policy-arn "$X"` answered
# NoSuchEntity with empty stdout - which read back as an EMPTY ownership
# marker two layers away. #1206 was filed against the stamp; the stamp was
# never wrong.
#
# Dropping --query is what fixes it, exactly as in gauntlet_tagged_count:
# with --output json and no --query, the CLI's automatic pagination merges
# every page into one document before anything filters it, so jq here sees
# the whole listing. Never add --query back to this call.
#
# Usage - the direct replacement for the `[?...] | [0]` idiom:
#
#   # was: awsl iam list-policy-tags --policy-arn "$ARN" \
#   #        --query "Tags[?Key=='tofu-address'].Value | [0]" --output text
#   ADDR="$(gauntlet_first_match '.Tags[] | select(.Key == "tofu-address") | .Value' \
#            awsl iam list-policy-tags --policy-arn "$ARN")"
#
# TWO DIFFERENCES from the idiom it replaces, both deliberate:
#
#   1. No match prints NOTHING, where `--query ... | [0] --output text`
#      printed the literal string "None". Test the result with
#      `[ -n "$X" ]`, not `[ "$X" != "None" ]`. "None" is a value the shell
#      cannot distinguish from a tag whose value really is "None", and
#      carrying it forward is half of what #1206 cost.
#   2. The selector is jq, not JMESPath, because the pages are already
#      merged into JSON by the time it runs.
gauntlet_first_match() {
  local selector="$1"; shift
  "$@" --output json | jq -r "[ $selector ] | .[0] // empty"
}

# ── counting an estate's objects where GetResources cannot see them (#1271) ──
#
# gauntlet_estate_objects <estate> <aws-invocation-prefix...>
#
# Collects every object in the account that carries tofu-estate=<estate>,
# reading BOTH the Resource Groups Tagging API and IAM's own per-resource
# tag APIs, and deduplicating by ARN. <aws-invocation-prefix> is whatever
# reaches the target - a script's own `awsl` function, or a literal
# `aws --endpoint-url ... --region ...` - and this helper appends the
# subcommands itself, because it makes several different calls.
#
# WHY THIS EXISTS (issue #1271). `resourcegroupstaggingapi get-resources`
# does not index IAM on the pinned emulator, and real AWS does not index
# several IAM types there either (#1134). Probed against
# ghcr.io/lex00/floci@sha256:0bbeb430 on 2026-09-17, one container: a
# customer-managed policy, a role and an instance profile, each created
# with `tofu-estate=probe-estate`, all three read that tag back through
# `iam:ListPolicyTags` / `ListRoleTags` / `ListInstanceProfileTags`, and
# GetResources filtered to the same tag returned ONE object - an S3 bucket
# tagged identically in the same container. The four IAM objects were
# absent.
#
# So `gauntlet_tagged_count ... get-resources --tag-filters
# Key=tofu-estate,Values=$ESTATE` reads 0 for an IAM-only estate no matter
# what is marked, and the three assertions corpus-iam-policy hung off it
# were a failure (`expected 2, got 0`) and two checks that could not fail
# (`0 unmarked, good`, `0 objects before, 0 after`). A count that returns
# the same number for every possible state of the world is not a
# measurement. This helper is the instrument that can answer.
#
# CORRECT UNDER BOTH PINS, deliberately. lex00/floci#206 will make
# GetResources serve `iam:policy` and `iam:instance-profile` in us-east-1
# (#1152). The union is deduplicated by ARN, so an object both routes
# return is counted ONCE: the total this helper reports does not move when
# that image is pinned. GAUNTLET_ESTATE_BOTH_N below is how a reader tells
# which world the run happened in - it is 0 on the current pin and rises
# when GetResources starts answering. An assertion written against
# GAUNTLET_ESTATE_N therefore means the same thing before and after the
# repin; one written against GAUNTLET_ESTATE_RGTA_N does not, and should
# not be written.
#
# WHAT THE NATIVE LEG COVERS, exactly: customer-managed IAM policies
# (`--scope Local`), IAM roles, and IAM instance profiles. NOT IAM users,
# groups, OIDC/SAML providers or server certificates - an estate holding
# any of those needs a fourth leg added here, and will otherwise be
# undercounted the same way this issue describes. AWS-managed policies are
# excluded on purpose: floci serves 1568 of them and none can carry an
# ownership marker.
#
# SETS GLOBALS, does not print. Call it as a statement, never in a command
# substitution - `$(...)` runs it in a subshell and the split counts are
# lost, which is the mistake gauntlet_ec2_tagged_count's own comment in
# live/e2e/corpus-ec2-instance-complete/run.sh already records:
#
#   GAUNTLET_ESTATE_ARNS     one ARN per line, sorted, deduplicated
#   GAUNTLET_ESTATE_N        how many distinct ARNs that is - the count
#   GAUNTLET_ESTATE_RGTA_N   how many GetResources returned
#   GAUNTLET_ESTATE_IAM_N    how many IAM's own tag APIs returned
#   GAUNTLET_ESTATE_BOTH_N   how many BOTH routes returned (0 before #1152)
#
# RETURNS NON-ZERO, loudly, if any call fails, and leaves the globals
# untouched. The idiom this replaces ended in `2>/dev/null || echo 0`,
# which turned an unreachable endpoint into "0 objects, nothing is marked,
# good" - a second way the same assertions could not fail. Callers write
# `gauntlet_estate_objects "$ESTATE" awsl || fail "..."`.
gauntlet_estate_objects() {
  local estate="$1"; shift
  [ -n "$estate" ] || { printf 'gauntlet_estate_objects: no estate name given\n' >&2; return 2; }
  [ "$#" -gt 0 ] || { printf 'gauntlet_estate_objects: no AWS CLI invocation prefix given\n' >&2; return 2; }

  local rgta iam both all
  rgta="$(_gauntlet_rgta_estate_arns "$estate" "$@")" || return 1
  iam="$(_gauntlet_iam_estate_arns "$estate" "$@")" || return 1

  # Both lists are already sorted and unique, so a duplicate across the two
  # is exactly an ARN both routes returned.
  # awk 'NF' rather than `grep -v '^$'` to drop the blanks: grep exits 1 when
  # it prints nothing, and under `set -o pipefail` that made an estate with
  # ZERO objects come back as a refusal instead of a zero - found by running
  # the empty case against a live container, not by reading the code. Zero is
  # the answer cold_deploy's "nothing is marked yet" assertion needs most.
  both="$(printf '%s\n%s\n' "$rgta" "$iam" | awk 'NF' | sort | uniq -d)"
  all="$(printf '%s\n%s\n' "$rgta" "$iam" | awk 'NF' | sort -u)"

  GAUNTLET_ESTATE_ARNS="$all"
  GAUNTLET_ESTATE_RGTA_N="$(_gauntlet_count_lines "$rgta")"
  GAUNTLET_ESTATE_IAM_N="$(_gauntlet_count_lines "$iam")"
  GAUNTLET_ESTATE_BOTH_N="$(_gauntlet_count_lines "$both")"
  GAUNTLET_ESTATE_N="$(_gauntlet_count_lines "$all")"
}

# _gauntlet_count_lines <text>: how many non-blank lines it holds.
_gauntlet_count_lines() {
  printf '%s\n' "$1" | awk 'NF' | wc -l | tr -d ' '
}

# _gauntlet_rgta_estate_arns <estate> <aws-prefix...>: the ARNs the Resource
# Groups Tagging API returns for this estate, sorted and unique. No --query,
# for gauntlet_tagged_count's reason (#1042): the CLI applies --query per
# page, so filtering has to happen after the pages are merged.
_gauntlet_rgta_estate_arns() {
  local estate="$1"; shift
  local out
  out="$("$@" resourcegroupstaggingapi get-resources \
    --tag-filters "Key=tofu-estate,Values=$estate" --output json)" \
    || { printf 'gauntlet_estate_objects: `resourcegroupstaggingapi get-resources` failed against this target\n' >&2; return 1; }
  jq -r '.ResourceTagMappingList[].ResourceARN' <<< "$out" | sort -u
}

# _gauntlet_iam_estate_arns <estate> <aws-prefix...>: the ARNs of the IAM
# objects carrying tofu-estate=<estate>, read through IAM's own tag APIs -
# the route #1125 wired into the sweep, and the only one that answers for
# these types on the current pin.
_gauntlet_iam_estate_arns() {
  local estate="$1"; shift
  local out found="" name arn

  out="$("$@" iam list-policies --scope Local --output json)" \
    || { printf 'gauntlet_estate_objects: `iam list-policies --scope Local` failed against this target\n' >&2; return 1; }
  # A process substitution, not a pipe: a pipe would run the loop in a
  # subshell and `found` would come back empty.
  while IFS= read -r arn; do
    [ -n "$arn" ] || continue
    _gauntlet_iam_tag_hit "$estate" "$@" iam list-policy-tags --policy-arn "$arn" \
      && found="$found$arn"$'\n'
  done < <(jq -r '.Policies[].Arn' <<< "$out")

  out="$("$@" iam list-roles --output json)" \
    || { printf 'gauntlet_estate_objects: `iam list-roles` failed against this target\n' >&2; return 1; }
  while IFS=$'\t' read -r name arn; do
    [ -n "$name" ] || continue
    _gauntlet_iam_tag_hit "$estate" "$@" iam list-role-tags --role-name "$name" \
      && found="$found$arn"$'\n'
  done < <(jq -r '.Roles[] | .RoleName + "\t" + .Arn' <<< "$out")

  out="$("$@" iam list-instance-profiles --output json)" \
    || { printf 'gauntlet_estate_objects: `iam list-instance-profiles` failed against this target\n' >&2; return 1; }
  while IFS=$'\t' read -r name arn; do
    [ -n "$name" ] || continue
    _gauntlet_iam_tag_hit "$estate" "$@" iam list-instance-profile-tags --instance-profile-name "$name" \
      && found="$found$arn"$'\n'
  done < <(jq -r '.InstanceProfiles[] | .InstanceProfileName + "\t" + .Arn' <<< "$out")

  printf '%s' "$found" | awk 'NF' | sort -u
}

# _gauntlet_iam_tag_hit <estate> <full aws tag-listing invocation...>: true
# when that object carries tofu-estate=<estate>. An object whose tags cannot
# be read at all is NOT a hit and is not an error either - it is an object
# this estate does not own, and IAM answers NoSuchEntity for plenty of them.
_gauntlet_iam_tag_hit() {
  local estate="$1"; shift
  local tags
  tags="$("$@" --output json 2>/dev/null)" || return 1
  [ -n "$tags" ] || return 1
  [ "$(jq -r --arg e "$estate" \
    '[.Tags[]? | select(.Key == "tofu-estate" and .Value == $e)] | length' <<< "$tags")" != "0" ]
}

# ── the cold-deploy pre-apply (#1173) ────────────────────────────────────
#
# Some configurations cannot be planned in one pass. A root that declares a
# CustomResourceDefinition and an object of that CRD is the case that forced
# this: kubernetes_manifest builds the object's schema at PLAN time, so the
# plan fails before anything is created, `depends_on` does not help, and
# re-running fails identically forever. `terraform apply -target=<the CRD>`
# followed by a plain apply is what an operator does, and it is what stock
# terraform has to do with the same configuration.
#
# The ruling (#1173, 2026-09-16) is that the stage grows a DECLARED pre-apply
# and the stock oracle performs the identical one:
#
#   1. the addresses live in live/gauntlet/estates.json's `pre_apply`, not in
#      the script, so the artifact and live/GAUNTLET.md can record them;
#   2. every side runs the SAME list - which is why gauntlet_pre_apply takes
#      all the sides in one call and refuses a single-sided one, rather than
#      being called once per side where a copy-paste could diverge them;
#   3. the cold_deploy verdict line names the pre-apply and its addresses
#      (gauntlet_pre_apply_note), and tools/gauntlet's own runner fails the
#      stage if it does not (preApplyVerdictGap, run.go);
#   4. an estate that declares no pre_apply never calls any of this and is
#      exactly what it was.

# gauntlet_pre_apply_targets <estate>: prints the declared addresses, one per
# line, straight out of the manifest. Prints nothing and succeeds when the
# estate declares none; fails when the estate is not in the manifest at all,
# because a script asking for a list under a name the manifest does not know
# is a typo, not an empty declaration.
gauntlet_pre_apply_targets() {
  : "${ROOT:?gauntlet_pre_apply_targets needs \$ROOT set (every crossing script sets it before sourcing this file)}"
  ESTATE_NAME="$1" python3 - "$ROOT/live/gauntlet/estates.json" <<'PY'
import json, os, sys
name = os.environ["ESTATE_NAME"]
with open(sys.argv[1]) as f:
    m = json.load(f)
for e in m["estates"]:
    if e["name"] == name:
        for addr in e.get("pre_apply", []):
            print(addr)
        break
else:
    sys.stderr.write("gauntlet_pre_apply_targets: %s is not in live/gauntlet/estates.json\n" % name)
    sys.exit(1)
PY
}

# gauntlet_pre_apply_reason <estate>: the manifest's pre_apply_reason.
gauntlet_pre_apply_reason() {
  : "${ROOT:?gauntlet_pre_apply_reason needs \$ROOT set}"
  ESTATE_NAME="$1" python3 - "$ROOT/live/gauntlet/estates.json" <<'PY'
import json, os, sys
name = os.environ["ESTATE_NAME"]
with open(sys.argv[1]) as f:
    m = json.load(f)
for e in m["estates"]:
    if e["name"] == name:
        print(e.get("pre_apply_reason", ""))
        break
else:
    sys.exit(1)
PY
}

# gauntlet_pre_apply <estate> <label>:<function> <label>:<function> [...]
#
# Runs the estate's declared pre-apply on every side named, in order, by
# calling each side's shell function with the `-target=<addr>` arguments
# appended to whatever that function already does. A side function is
# whatever "apply in this root against this cluster" means for that side:
#
#   pre_apply_estate() { ( cd "$EST"    && KUBECONFIG="$KCA" terraform apply -auto-approve -input=false -no-color "$@" ); }
#   pre_apply_oracle() { ( cd "$ORACLE" && KUBECONFIG="$KCB" terraform apply -auto-approve -input=false -no-color "$@" ); }
#   gauntlet_pre_apply "$ESTATE" estate:pre_apply_estate oracle:pre_apply_oracle
#
# Two sides is the minimum and the refusal is the point: a crossing where
# choudoufu's side got a targeted pre-apply and the stock oracle did not is
# not a comparison of like with like, so this function will not perform one.
#
# Returns non-zero (and says which side) if any side's apply fails; the
# caller's own fail() records the stage.
gauntlet_pre_apply() {
  local estate="$1"; shift
  local addrs spec side fn
  addrs="$(gauntlet_pre_apply_targets "$estate")" || return 1
  if [ -z "$addrs" ]; then
    printf 'gauntlet_pre_apply: estate %s declares no pre_apply in live/gauntlet/estates.json - declare the addresses there rather than targeting them from the script (#1173)\n' "$estate" >&2
    return 1
  fi
  if [ "$#" -lt 2 ]; then
    printf 'gauntlet_pre_apply: %s side(s) given; a pre-apply needs at least two - the estate and its stock oracle. A pre-apply one side performs and the other does not makes the crossing meaningless (#1173)\n' "$#" >&2
    return 1
  fi
  local targets=() a
  while IFS= read -r a; do
    [ -n "$a" ] || continue
    targets+=("-target=$a")
  done <<< "$addrs"
  local ran=""
  local csv=""
  for spec in "$@"; do
    side="${spec%%:*}"; fn="${spec#*:}"
    if [ -z "$side" ] || [ -z "$fn" ] || [ "$side" = "$spec" ]; then
      printf 'gauntlet_pre_apply: side %q must be <label>:<function>\n' "$spec" >&2
      return 1
    fi
    if ! declare -F "$fn" >/dev/null 2>&1; then
      printf 'gauntlet_pre_apply: side %s names %s, which is not a shell function\n' "$side" "$fn" >&2
      return 1
    fi
    printf '  pre-apply on %s: %s %s\n' "$side" "$fn" "${targets[*]}"
    if ! "$fn" "${targets[@]}"; then
      printf 'gauntlet_pre_apply: the declared pre-apply failed on side %s (%s)\n' "$side" "$fn" >&2
      return 1
    fi
    ran="${ran:+$ran,}$side"
  done
  csv="$(printf '%s' "$addrs" | tr '\n' ',' | sed 's/,$//')"
  _GAUNTLET_PRE_APPLY_ESTATE="$estate"
  _GAUNTLET_PRE_APPLY_COUNT="$(printf '%s\n' "$addrs" | grep -c .)"
  _GAUNTLET_PRE_APPLY_SIDES="$ran"
  # The runner's own record of what was pre-applied, emitted by the function
  # that did it rather than typed into a verdict line by the script. This is
  # what preApplyVerdictGap checks the declared list against, per address,
  # which is why the verdict sentence below only has to carry a count
  # (#1173, the sentence corrected 2026-09-16). Values carry no spaces: the
  # protocol only lets `detail` run to the end of a line.
  printf 'GAUNTLET pre_apply=%s sides=%s\n' "$csv" "$ran"
}

# gauntlet_pre_apply_note: one sentence naming the pre-apply that actually
# ran, for the script to interpolate into its cold_deploy detail. Every
# number in it comes from the run gauntlet_pre_apply just performed, never
# from anything typed twice. Prints nothing and fails if no pre-apply ran,
# so a script cannot claim one it did not perform.
#
# It names a COUNT and the manifest field, not the addresses. The first
# spelling of the rule put every address in the verdict line, and on
# cert-manager that was a 3.3KB sentence: it satisfied "the verdict line
# names its addresses" and defeated the reason for the rule, which is that
# a reader must SEE that two applies happened without opening the log.
# Corrected 2026-09-16. The addresses did not stop being checked - they are
# checked against the GAUNTLET pre_apply= line above, which the function
# that performed them emits, rather than against prose.
gauntlet_pre_apply_note() {
  if [ -z "${_GAUNTLET_PRE_APPLY_SIDES:-}" ]; then
    printf 'gauntlet_pre_apply_note: no pre-apply has run in this script; call gauntlet_pre_apply first\n' >&2
    return 1
  fi
  local reason
  reason="$(gauntlet_pre_apply_reason "$_GAUNTLET_PRE_APPLY_ESTATE")"
  printf 'declared pre-apply, #1173: %s address(es) declared at pre_apply in live/gauntlet/estates.json, applied with -target on every side (%s) before the main apply - the full list is in that file and in the GAUNTLET pre_apply= line this run printed, which the runner checks address by address; forced by: %s' \
    "$_GAUNTLET_PRE_APPLY_COUNT" "$_GAUNTLET_PRE_APPLY_SIDES" "$reason"
}

# gauntlet_wait_until <timeout-seconds> <what> -- <command...>
#
# Polls <command> every 2 seconds until it succeeds, giving up after
# <timeout-seconds>. On timeout it prints a loud, named failure and returns
# 1 rather than letting the caller fall through into whatever confusing
# error the not-yet-ready thing produces next - #1173's "the wait must be
# bounded and must fail loudly on timeout". cert-manager's two webhooks are
# failurePolicy: Fail, so an object admitted through a webhook that exists
# but is not yet serving is REJECTED, and "the CRD is installed" is not the
# same fact as "the webhook in front of it answers".
gauntlet_wait_until() {
  local timeout="$1" what="$2"; shift 2
  [ "${1:-}" = "--" ] || { printf 'gauntlet_wait_until: expected -- before the command\n' >&2; return 1; }
  shift
  local deadline=$(( $(date +%s) + timeout )) tries=0
  while :; do
    tries=$((tries + 1))
    if "$@" >/dev/null 2>&1; then
      printf '  ready after %ss (%s attempt(s)): %s\n' "$(( $(date +%s) - deadline + timeout ))" "$tries" "$what"
      return 0
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      printf 'gauntlet_wait_until: TIMEOUT after %ss waiting for %s (%s attempt(s)); last attempt:\n' "$timeout" "$what" "$tries" >&2
      "$@" >&2 2>&1 || true
      return 1
    fi
    sleep 2
  done
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

# gauntlet_print_evidence <text>
#
# Prints <text> whole. Issue #1158: a failure branch that greps a summary
# line out of its own captured output and prints only that discards
# everything the view printed underneath it - PR #1129 (terralith-scale's
# day2_remove, greping away the plan's "Not swept for removal" section) and
# PR #1157 (corpus-ec2-instance-complete's two foreign-object assertions,
# greping away the itemized `Foreign resources:` list) each cost a full
# re-run to even ask the question the missing section already answered. 24
# more scripts shared the exact same `grep -E '^Foreign resources:'` discard
# (PR #1157 enumerated 23 of them; corpus-mastino-dns was the 24th, missed
# by that search because an embedded NUL byte in one of its comments makes
# some grep implementations treat the file as binary and skip it silently -
# live/foreign_resources_evidence_guard_test.go scans with Go's os.ReadFile
# instead, precisely so this cannot happen again). This is the one helper
# they now call instead of 24 copies of it.
#
# Call it with the WHOLE captured output - a variable, or "$(cat "$file")"
# for a file-backed capture - never with a `grep` of it: grepping first is
# exactly the discard this function exists to stop, and this function
# cannot recover what its caller already filtered away.
#
# It takes the text as an ARGUMENT, not as a format string: `printf '%s\n'
# "$1"` never interprets a `%` inside the evidence itself as a directive,
# where `printf "$1"` would silently eat or misprint part of it. That
# distinction matters more than usual here - a helper whose entire job is
# "show the caller everything" that quietly drops a slice of it would be
# this issue's own defect, one level up (see #1158's own caution about a
# sibling helper in corpus-iam-policy that echoed a value through command
# substitution and silently dropped part of it).
gauntlet_print_evidence() {
  printf '%s\n' "$1"
}
