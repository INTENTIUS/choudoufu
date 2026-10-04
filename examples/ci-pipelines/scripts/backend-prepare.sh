#!/usr/bin/env bash
# The three steps of the `backend-prepare` Op (src/backend-prepare.op.ts),
# GitHub issue #1832, #1244 ruling 3: stand the estate's record store bucket
# up through the pipeline instead of by someone running a justfile on a
# laptop.
#
#   scripts/backend-prepare.sh plan     # before the gate: read-only
#   scripts/backend-prepare.sh up       # after the gate: create or update
#   scripts/backend-prepare.sh verify   # after up: choudoufu's own contract check
#
# # Nothing here is a second copy of anything
#
# The bucket is declared once, in examples/record-store-bucket through chant's
# AWS lexicon, and stood up by that project's own `just up`. This script calls
# those recipes and carries no declaration and no refusal of its own beyond
# the account check below. That matters more than tidiness: `just up` reads
# the LIVE bucket before it deploys, keeps the noncurrent-version window it
# finds there, and refuses a run that would silently drop the bucket's KMS
# key (#1421). An Op that applied the built template directly would skip both,
# which is why #1244's first draft of this Op could not be revived.
#
# The bucket's name is read out of terraform/estate.chdf.hcl, the sidecar the
# estate's own runs read it from, the same way scripts/oidc-bootstrap.sh does.
# A RECORD_BUCKET variable on the pipeline would be a second spelling of that
# line, and the two would drift.
#
# # The account check
#
# The sidecar pins `bucket_owner`, which puts ExpectedBucketOwner on every
# request the estate makes (#1381). CloudFormation creates a bucket in
# whatever account the credentials belong to, so a backend role in the wrong
# account would stand up a bucket the estate then refuses on every run, or
# worse, find a name somebody else already holds. So before anything reads or
# writes, the caller's account must be the declared owner.
#
# # stdout
#
# `plan` prints exactly one line on stdout: the SHA256 of the CloudFormation
# template it built. The Op binds its approval gate to that value, so an
# approval is for a template, and a run whose template moved since is refused
# by name rather than applying a bucket nobody looked at. Everything else -
# the template itself included, for the reviewer - goes to stderr.
#
# Environment:
#   AWS_REGION              the region the bucket lives in (the workflow sets
#                           it from the repository variable every job reads)
#   RECORD_BUCKET_PROJECT   where examples/record-store-bucket is checked out;
#                           default ../record-store-bucket beside this project
#   RECORD_KMS_KEY_ARN      optional, passed through to `just up`: the
#                           customer managed key, when the bucket has one
#   RECORD_NONCURRENT_DAYS  optional, passed through: the recovery window
#
# Needs: aws, jq, just, npm, and choudoufu for `verify`. The generated
# workflow's setup step installs the first three.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXAMPLE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ROOT_DIR="$EXAMPLE_DIR/terraform"
ESTATE_FILE="${BACKEND_PREPARE_ESTATE_FILE:-$ROOT_DIR/estate.chdf.hcl}"
BUCKET_PROJECT="${RECORD_BUCKET_PROJECT:-$EXAMPLE_DIR/../record-store-bucket}"

usage() {
  echo "usage: $0 plan|up|verify" >&2
  exit 2
}

die() {
  echo "backend-prepare: $*" >&2
  exit 1
}

[ $# -eq 1 ] || usage
stage="$1"
case "$stage" in plan|up|verify) ;; *) usage ;; esac

# The record_store "s3" block's bucket and bucket_owner, read out of the
# sidecar. The same sed oidc-bootstrap.sh uses for the bucket, so the two
# scripts cannot read two different names out of one file.
[ -f "$ESTATE_FILE" ] || die "no estate sidecar at $ESTATE_FILE"
grep -qE '^[[:space:]]*record_store[[:space:]]+"s3"' "$ESTATE_FILE" \
  || die "$ESTATE_FILE declares no record_store \"s3\" block, so there is no bucket to prepare"
BUCKET="$(sed -nE 's/^[[:space:]]*bucket[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$ESTATE_FILE")"
OWNER="$(sed -nE 's/^[[:space:]]*bucket_owner[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$ESTATE_FILE")"
[ -n "$BUCKET" ] || die "could not read record_store \"s3\"'s bucket out of $ESTATE_FILE"
case "$BUCKET" in
  *$'\n'*) die "$ESTATE_FILE names more than one bucket; this script prepares exactly one" ;;
esac
[ -n "${AWS_REGION:-}" ] \
  || die "AWS_REGION is unset. The bucket is created in a region, and guessing one is how a bucket ends up where the estate's runs never look"

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is not on PATH; $2"
}

# The bucket project's own recipes, run in its own directory so its
# justfile's relative paths (src/, templates/, iam/) resolve the way they do
# for somebody running `just up` there by hand.
bucket_just() {
  ( cd "$BUCKET_PROJECT" && just "$@" )
}

# Install the bucket project's own dependencies (it pins its own chant, which
# need not be this project's). npm ci is a clean install from its lockfile.
bucket_deps() {
  [ -f "$BUCKET_PROJECT/justfile" ] \
    || die "no record-store-bucket project at $BUCKET_PROJECT. Check it out beside this project, or set RECORD_BUCKET_PROJECT"
  ( cd "$BUCKET_PROJECT" && npm ci --no-audit --no-fund >&2 )
}

# The caller must be the account the sidecar declares as the owner. With no
# bucket_owner declared there is nothing to compare against, and that is
# said rather than passed over.
check_account() {
  local caller
  caller="$(aws sts get-caller-identity --query Account --output text)" \
    || die "could not read the caller's account. Nothing was read or written"
  if [ -z "$OWNER" ]; then
    echo "backend-prepare: $ESTATE_FILE declares no bucket_owner; preparing $BUCKET in the caller's account $caller" >&2
    return
  fi
  if [ "$caller" != "$OWNER" ]; then
    echo "REFUSING to prepare $BUCKET." >&2
    echo "$ESTATE_FILE declares bucket_owner = \"$OWNER\", and these credentials are account $caller." >&2
    echo "CloudFormation would create the bucket in $caller, and every run of the estate would then" >&2
    echo "refuse it, because each request it makes carries ExpectedBucketOwner=$OWNER." >&2
    echo "Point the backend role at account $OWNER, or change bucket_owner on purpose." >&2
    exit 1
  fi
}

case "$stage" in
  plan)
    need aws "the workflow's setup step installs it"
    need jq "the workflow's setup step installs it"
    need just "the workflow's setup step installs it"
    need npm "the job image carries it"
    check_account
    bucket_deps
    echo "backend-prepare: building the template for s3://$BUCKET in $AWS_REGION" >&2
    template="$(bucket_just plan "$BUCKET")"
    [ -n "$template" ] || die "\`just plan $BUCKET\` built an empty template"
    printf '%s\n' "$template" >&2
    # The one stdout line: what the gate is asked to approve.
    printf '%s' "$template" | sha256sum | awk '{print "sha256:" $1}'
    ;;

  up)
    need aws "the workflow's setup step installs it"
    need jq "the workflow's setup step installs it"
    need just "the workflow's setup step installs it"
    need npm "the job image carries it"
    check_account
    [ -d "$BUCKET_PROJECT/node_modules" ] || bucket_deps
    # `just up` is the whole apply: it re-reads the live bucket, refuses an
    # encryption downgrade, keeps the live retention window, deploys, and
    # prints RECORD_STORE_BUCKET. Its output goes to stderr so this step's
    # result stays the one line below.
    bucket_just up "$BUCKET" >&2
    echo "RECORD_STORE_BUCKET=$BUCKET"
    ;;

  verify)
    need choudoufu "every job installs a pinned release before it runs an Op"
    # Asked from the estate's own root, with no -bucket, so the answer is the
    # one the estate's runs will get: checked as this estate's key
    # namespaces, under the sidecar's bucket_owner. `just verify` asks the
    # same binary with -bucket, which credits only an unfiltered lifecycle
    # rule (#1377) and pins no owner. live-bucket exits 1 on a bucket that
    # fails its contract, and that fails this step.
    ( cd "$ROOT_DIR" && choudoufu live-bucket )
    ;;
esac
