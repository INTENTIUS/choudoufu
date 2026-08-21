#!/usr/bin/env bash
set -uo pipefail

# A real third-party estate crossed against a real emulator: issue #274's
# five-stage pipeline, for .corpus/mastino/global/dns.
#
# DataCite's own global DNS root module (datacite/mastino, the repository
# that runs datacite.org). 54 resource blocks, 63 instances:
#
#   4  aws_route53_zone     production (public datacite.org), internal
#                            (PRIVATE datacite.org, VPC-associated), com
#                            (datacite.com), eu (datacite.eu)
#   59 aws_route53_record   50 blocks, one of them count = 10
#
# It is the second-largest of #274's twenty-eight offline-clean estates and
# the largest that had never touched a cloud; the largest,
# simpleinfra/team-members-access, is the one crossing #274 opened with.
#
# WHAT THIS ESTATE CONTRIBUTES THAT NOTHING ALREADY CROSSED DOES
#
#   1. TWO LIVE ZONES WITH THE SAME NAME. `production` and `internal` are
#      both called datacite.org - one public, one private and associated
#      with a VPC. aws_route53_zone is ServerAssigned, so the only thing
#      that can tell a stateless replan which of the two a block owns is
#      the tofu-address marker on the zone itself. corpus-root-dns-zones
#      crossed two zones of this type but with DIFFERENT names, where a
#      name-based guess would also have worked; here it cannot.
#      table_generated.go's own row says so: "two zones may carry the same
#      name."
#
#   2. 59 UNTAGGABLE CHILDREN OF A SERVER-ASSIGNED PARENT. Route 53 record
#      sets carry no tags at all. Every one of the 59 derives its identity
#      as ZONEID_NAME_TYPE, and the ZONEID half can only come from the
#      parent zone's marker. That is the invariant's "tagged, plus
#      derived-from-tagged" at a ratio of 4 to 59 - by far the widest
#      derivation fan-out of any crossing so far.
#
#   3. count.index ARITHMETIC IN AN IDENTITY-BEARING ARGUMENT.
#      `wp-prod-staging` is count = 10 with
#      name = "staging${count.index + 3}.datacite.org", so ten instances
#      whose identities differ only by an offset index. HANDOFF records a
#      shipped defect where `count.index % 3` collapsed two instances onto
#      one live identity; `+ 3` is injective, and stage 3 below asserts all
#      ten rendered identities are distinct and are staging3..staging12.
#
#   4. A SIBLING REFERENCE TO A NON-IDENTITY ATTRIBUTE.
#      `mx-datacite` and `txt-datacite` set name = aws_route53_zone.
#      production.name - the zone's DOMAIN NAME, which is not one of that
#      type's identity attributes (those are id/zone_id). Both sites are
#      refused by refusal-probe's schema-LESS mode ("Not an identity
#      attribute", main.tf:91 and main.tf:105) and clear once the provider
#      schema is loaded, which is exactly the asymmetry HANDOFF's
#      "Measuring" section warns about. Stage 3 asserts both rendered
#      identities by value.
#
# STAGES:
#   1. COLD DEPLOY   plain `terraform apply` (real Terraform, no choudoufu,
#                     no live block) - the honest proof the estate is real
#                     and buildable, and the source of genuinely unmarked
#                     live infrastructure stage 2 adopts.
#   2. MIGRATE        `choudoufu live-import -approve` against that cold
#                     state. 4 of 63 are taggable; 59 are correctly
#                     UNTAGGABLE.
#   3. TEST PLAN      delete the state file, `choudoufu live-plan`, assert
#                     the plan is EMPTY and assert all 63 rendered identity
#                     strings against the AWS CLI's own answer.
#   4. TEST APPLY     apply the empty plan; a genuine no-op, asserted by
#                     comparing live zone and record-set counts, the four
#                     markers, and the residue records either side of it.
#   5. DRIFT AND      mutate one UNTAGGABLE, RESIDUE-CARRYING record set
#      RECONVERGE     out of band and assert the plan proposes fixing
#                     exactly that object and exactly one attribute.
#
# All five stages pass, as of 2026-08-20 and #341's fix.
#
# WHAT IT TOOK TO GET HERE: this crossing landed at 2 of 5 on 2026-08-19,
# stage 3 blocked, and the section below is why. It is kept in full rather
# than deleted, because the defect it names covers a third of every type
# this fork admits and the shape of it is worth not re-learning.
#
# THE WALL THIS CROSSING FOUND (stage 1, and it is the estate's own, not
# choudoufu's and not floci's):
#
#   The four `*-ns` blocks manage the NS record set at each zone's own
#   apex. Route 53 creates that record set itself the instant the zone
#   exists, so `terraform apply` from an empty account cannot CREATE it:
#
#     Error: creating Route53 Record: ... InvalidChangeBatch: Tried to
#     create resource record set [name='datacite.org.', type='NS'] but it
#     already exists.
#
#   Reproduced for real against floci before this delta was written - a
#   two-resource reduction (one zone, one apex NS record, allow_overwrite
#   left at its default) under stock OpenTofu 1.12.5 and aws 6.59.0 - and
#   floci's error string is byte-identical to the one real Route 53
#   returns. The estate's live TFC workspace has had these four
#   in state since before hashicorp/aws flipped allow_overwrite's default
#   to false in 3.0, so its own author never sees this; a from-scratch
#   apply of the published text does. DELTA 5 sets
#   `allow_overwrite = true` on exactly those four blocks - the same
#   argument the estate's own author already writes on `wp-prod-staging`
#   ("3 is just where we started from for existing records"), for exactly
#   this reason. It is an argument-level change and it touches nothing
#   identity-bearing: allow_overwrite is not a component of
#   aws_route53_record's identity row.
#
# THE SECOND WALL THIS CROSSING FOUND - #341, FIXED 2026-08-20 (stage 3,
# and it WAS choudoufu's - already-built machinery a migrate could not
# reach):
#
#   With a `live` block and nothing else, the first cold live-plan came
#   back with 14 records proposed for an in-place update and the whole
#   diff was one line each:
#
#     # aws_route53_record.wp-prod-staging[0] will be updated in-place
#     ~ resource "aws_route53_record" "wp-prod-staging" {
#         + allow_overwrite = true
#           id              = "ZPDU1RN465K08IR_staging3.datacite.org_A"
#           name            = "staging3.datacite.org"
#       }
#
#   `allow_overwrite` is a pure input: Route 53 has no such field, so no
#   read ever gives it back. Stock Terraform never notices because the
#   state file remembers what was sent; #73 deletes the state file, so
#   every cold plan re-proposes the identical update and applying it does
#   not settle it. This is #275's residue class exactly, and
#   internal/live/projection/residue.go's own doc comment already names
#   this attribute by name.
#
#   ADDING A record_store DOES NOT FIX IT, AND THAT IS THE DEFECT. DELTA 6
#   below declares `record_store "local"`, and the store IS populated and
#   IS read back - the plan's own trace says
#
#     projection: filled 1 residue attribute(s) of aws_route53_zone.com
#       from the record store
#
#   for all four ZONES and for nothing else. Not one of the 59 record sets
#   has a residue record, and the reason is structural rather than
#   type-specific:
#
#     internal/live/liveimport/ratify.go, ratifyOne():
#       if !taggable(schema.Block) {
#           entry.Status = StatusUntaggable
#           ...
#           return entry, nil          // <- no *eligible is built
#       }
#
#   returns BEFORE the ReadResource that builds the `*eligible` carrying
#   the provider connection, schema and live value. Approve() then does
#
#       elig, ok := r.eligible[entry.Addr.String()]
#       if !ok { ...OutcomeSkipped...; continue }
#       ...
#       diags = diags.Append(recordResidueFor(ctx, r.residueStore, ...))
#
#   so `recordResidueFor` is unreachable for every untaggable resource.
#   `*eligible` is the carrier for two unrelated jobs - "write the tag" and
#   "record the residue" - and untaggability only disqualifies the first.
#   #327's own doc comment states the intent this misses: "the FIRST
#   live-plan after a clean migrate sees it null - a phantom update".
#
#   HOW GENERAL: recomputed 2026-08-20 at 7d9bd80f91 -
#   internal/live/identity/table_generated.go holds 1040 rows, of which
#   live/survey-full.json's own taggable signal calls 683 taggable and 342
#   untaggable, and does not cover the remaining 15 at all. (#341's own
#   figure said "342 of 1025"; 1025 is 683 + 342, so the untaggable count
#   was right and the denominator dropped the 15 uncovered rows.) Every one
#   of the 342 was excluded from migrate-time residue recording the same
#   way. How many of them actually carry a residue-shaped argument is a
#   behavioural question only classifyResidue's two-read probe can answer,
#   so that is not claimed here - aws_route53_record is simply the first
#   one measured to need it. Filed as #341.
#
#   TEN OF THE FOURTEEN ARE THE ESTATE'S OWN. `wp-prod-staging` carries
#   `allow_overwrite = true` in DataCite's published text, not because of
#   anything this script does. The other four are DELTA 5's apex NS
#   blocks, so this crossing's own delta widened the population from ten to
#   fourteen but did not create it. The wall would have been hit by this
#   estate with no deltas at all beyond the live block.
#
#   THE FIX (#341, 2026-08-20). ratify.go now has a third carrier beside
#   *eligible and #340's *recordable: a *residuable, built for any ADMITTED
#   instance whose provider schema has no tags argument, and Approve calls
#   recordResidueFor with it. It keys on taggable(schema.Block) - the
#   provider's own schema, this run, not a list of type names - so it
#   reaches all 342 untaggable admitted types and any future one the moment
#   a provider grows it.
#
#   Three things the fix deliberately does NOT do, each of which this
#   script asserts:
#     * the verdict does not move. An untaggable instance is still
#       UNTAGGABLE and still SKIPPED, so stage 2's two summary lines read
#       exactly what they read before (18 crossing scripts assert them).
#     * no ReadResource is added at ratify time. The object handed forward
#       is the STATE's own, which is where a residue value lives by
#       definition - and reaching a read would let an untaggable instance
#       come back MISSING or DRIFTED and move those counts.
#     * nothing is recorded for the other 45 record sets. They declare no
#       residue-shaped argument, classifyResidue proves nothing about them,
#       and a residue record must never speak where the provider does.
#
#   MEASURED HERE, 2026-08-20: 14 residue records written at migrate time,
#   every one of them allow_overwrite = true read out of the store's own
#   files; 14 record sets AND the 4 zones filling residue on the cold
#   replan (four zones and nothing else was the bug's own signature); the
#   plan EMPTY; a no-op apply that leaves all 14 records intact; and a TTL
#   drift on one of the fourteen reconverging to exactly that one instance
#   and exactly the ttl attribute - which is the check that a residue fill
#   has not started masking real live drift.
#
# ONE FLOCI DIVERGENCE, NOTED AND NOT WORKED AROUND (it does not block):
#   `lovable-verify` sets name = "_lovable.strategy", which is not inside
#   datacite.org. MEASURED HERE: floci accepts the record and stores it as
#   `_lovable.strategy.`, a name outside the zone it sits in. Route 53's
#   own documented rule is that a record's name must be within the hosted
#   zone, and it rejects one that is not with "RRSet with DNS name ... is
#   not permitted in zone ..." - documented behaviour, not something this
#   run could measure without a real account. So this crossing certifies
#   one record that real AWS would probably refuse to create at all.
#   The estate is left byte-for-byte rather than corrected - choudoufu
#   derives the same identity either way, and this crossing's job is to
#   measure choudoufu, not to fix DataCite's zone file - but the
#   divergence is recorded because a permissive emulator is how a crossing
#   passes here and fails there. Filed as lex00/floci#81 with the
#   AWS-CLI-only reproduction.
#
#   bash live/e2e/corpus-mastino-dns/run.sh
#
# Needs Docker, the AWS CLI, stock `terraform` on PATH for stage 1, and a
# populated .corpus (`just corpus-fetch`).
#
# RUN TIME. This script runs TWO provider inits and they cannot share a
# lock file: stage 1 is stock terraform (registry.terraform.io) and stages
# 2-5 are choudoufu (registry.opentofu.org), so the shared plugin cache
# below saves the download but not the checksum computation an init in a
# directory with no .terraform.lock.hcl performs. Measured 2026-08-20: a
# cold full run does not fit inside ten minutes. Neither init is optional
# and neither can be seeded from the other; budget accordingly, or run it
# with no wall-clock cap.
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4731, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string ahead of
#                stage 3's assertion, proving it is load-bearing rather
#                than a grep that always matches.
#   BREAK_STAGE5 set to 1 to drift a SECOND record set in stage 5. The
#                stage then requires the plan to propose more than one fix,
#                which is what makes the ordinary run's "exactly one"
#                assertion load-bearing. Verified 2026-08-20: the plan
#                reports "Plan: 0 to add, 2 to change, 0 to destroy".
#   DEBUG_KEEP   set to 1 to skip the exit trap: the floci container and
#                the WORK directory are left behind for inspection.
#
# .corpus is shared across every worktree and is NEVER written to: the
# estate is copied out first and every delta below lands on the copy.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CORPUS_DIR="${CORPUS_DIR:-$ROOT/.corpus}"
SRC="$CORPUS_DIR/mastino/global/dns"
WORK="$(mktemp -d)"
PLAIN="$WORK/plain"
EST="$WORK/estate"
FLOCI_PORT="${FLOCI_PORT:-4731}"
FLOCI_NAME="choudoufu-corpus-mastino-dns-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE_NAME="datacite-mastino-global-dns"
REGION="eu-west-1"

# This script runs TWO inits (the plain cold-deploy copy under stock
# terraform, the estate copy under choudoufu), each of which would otherwise
# re-download the AWS provider into its own scratch directory. Point both at
# the conventional shared plugin cache so only the first can ever pay for a
# download; an operator who already exports TF_PLUGIN_CACHE_DIR keeps theirs.
# The two copies cannot share a .terraform.lock.hcl the way an
# OpenTofu-native crossing's can - stage 1's registry is
# registry.terraform.io and the estate's is registry.opentofu.org - so a
# lock-file copy between them would name the wrong provider source for one
# side.
#
# #339: TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE sidesteps that limit
# entirely - real terraform and choudoufu both honor it, and each init
# consults the shared cache under its own registry-keyed path independently,
# so both halves of this crossing benefit, not just one. Without it, init in
# a directory with no .terraform.lock.hcl re-downloads the whole provider
# purely to compute checksums, even when the cache already holds that exact
# version (see live/e2e/README.md, "The shared plugin cache" for the measured numbers).
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$HOME/.terraform.d/plugin-cache}"
export TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1
mkdir -p "$TF_PLUGIN_CACHE_DIR"
BLOCKS=54
INSTANCES=63
TAGGABLE=4
UNTAGGABLE=59

# The record sets that carry a residue-shaped argument: the estate's own
# wp-prod-staging[0..9] (allow_overwrite = true in DataCite's published text)
# plus DELTA 5's four apex NS blocks. These fourteen are exactly the
# addresses that proposed "+ allow_overwrite = true" on every cold plan while
# #341 was open.
RESIDUE_WANT=14

# Stage 5's drift target, chosen on purpose from the fourteen rather than
# from the other 45: it is an UNTAGGABLE record set whose identity can only
# re-derive from its parent zone's marker, AND it is one whose prior state is
# partly filled from the record store. If the residue fill ever masked a real
# live change, this is where it would show, and stage 5 would come back empty
# instead of proposing exactly one fix.
DRIFT_ADDR='aws_route53_record.wp-prod-staging[0]'
DRIFT_RECORD_NAME="staging3.datacite.org"
DRIFT_RECORD_TYPE="A"
DRIFT_RECORD_VALUE="192.0.2.13"
WANT_TTL=300
DRIFT_TTL=77
# BREAK_STAGE5's second mutation, so the "exactly one instance" assertion has
# a negative control of its own.
BREAK_RECORD_NAME="staging4.datacite.org"

cleanup() {
  [ "${DEBUG_KEEP:-}" = "1" ] && { log "DEBUG_KEEP=1: leaving $FLOCI_NAME and $WORK"; return; }
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
awsl() { aws --endpoint-url "$ENDPOINT" --region "$REGION" "$@"; }

# ══════════════════════════════════════════════════════════════════════════
# 0. tools and corpus
# ══════════════════════════════════════════════════════════════════════════
log "=== 0. tools and corpus ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v terraform >/dev/null 2>&1 || fail "stock terraform is not on PATH - stage 1 needs it"
[ -d "$SRC" ] || fail "$SRC is missing - run 'just corpus-fetch' first"

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

STAGE="$WORK/staged"
mkdir -p "$STAGE"
cp "$SRC"/*.tf "$STAGE/"
for f in input.tf main.tf terraform.tf tld.tf var.tf; do
  [ -f "$STAGE/$f" ] || fail "the estate copy is missing $f - the corpus pin has moved"
done
GOT_BLOCKS="$(grep -h '^resource "' "$STAGE"/*.tf | grep -c .)"
[ "$GOT_BLOCKS" = "$BLOCKS" ] \
  || fail "the estate declares $GOT_BLOCKS resource blocks, expected $BLOCKS - the corpus pin has moved"
GOT_ZONES="$(grep -h '^resource "aws_route53_zone"' "$STAGE"/*.tf | grep -c .)"
[ "$GOT_ZONES" = "4" ] || fail "the estate declares $GOT_ZONES zones, expected 4 - the corpus pin has moved"
grep -q 'count           = 10' "$STAGE/main.tf" \
  || fail "wp-prod-staging is no longer count = 10 - the corpus pin has moved, and stage 3's ten-identity assertion is unchecked"
grep -qF 'name    = aws_route53_zone.production.name' "$STAGE/main.tf" \
  || fail "the sibling-name reference this crossing exists to exercise is gone - the corpus pin has moved"
log "  estate copied out of .corpus into $STAGE ($GOT_BLOCKS resource blocks, $GOT_ZONES zones)"

# ══════════════════════════════════════════════════════════════════════════
# 1. the deltas, applied to the copy
# ══════════════════════════════════════════════════════════════════════════
log "=== 1. onboarding deltas ==="

# DELTA 1+2: terraform.tf. The estate declares
#   cloud { organization = "datacite-ng" workspaces { name = "global-dns" } }
# A module may declare remote state or a live block, never both (#268 in
# its Terraform-Cloud form), so the cloud block goes. It is written out
# rather than regex-patched because the live block only exists in the
# choudoufu copy and stage 1 must run under stock Terraform, which has
# never heard of it.
grep -q 'cloud {' "$STAGE/terraform.tf" || fail "terraform.tf has no cloud block - the corpus pin has moved"
grep -q 'version = "~> 5"' "$STAGE/terraform.tf" \
  || fail "the aws constraint is no longer ~> 5, so DELTA 2's #269 justification is unchecked"
rm -f "$STAGE/terraform.tf"

# DELTA 3: emulator wiring on BOTH provider blocks - the default one and
# the unused `use1` us-east-1 alias, which OpenTofu configures regardless
# of whether anything references it. The estate's own access_key/secret_key
# variables carry the emulator's credentials (DELTA 4), so the provider
# blocks themselves keep their original argument list; only the flags with
# no environment-variable form are added.
perl -0pi -e 's{^provider "aws" \{\n}{provider "aws" \{\n  # DELTA 3 (emulator wiring)\n  skip_credentials_validation = true\n  skip_metadata_api_check     = true\n  s3_use_path_style           = true\n  endpoints \{\n    route53 = "ENDPOINT_URL"\n    ec2     = "ENDPOINT_URL"\n    sts     = "ENDPOINT_URL"\n  \}\n}gm' "$STAGE/input.tf"
PROV_N="$(grep -c 'DELTA 3' "$STAGE/input.tf")"
[ "$PROV_N" = "2" ] || { cat "$STAGE/input.tf"; fail "DELTA 3 patched $PROV_N provider blocks, expected 2"; }
perl -pi -e "s{ENDPOINT_URL}{$ENDPOINT}g" "$STAGE/input.tf"
grep -q "$ENDPOINT" "$STAGE/input.tf" || fail "DELTA 3 left the endpoint placeholder unsubstituted"
grep -qF 'data "aws_vpc" "datacite"' "$STAGE/input.tf" \
  || fail "DELTA 3 lost the estate's own aws_vpc data source"
log "  DELTA 3  emulator flags + endpoints on both provider blocks (incl. the unused use1 alias)"

# DELTA 5: allow_overwrite on the four apex NS blocks. See the header - the
# wall is real, was reproduced here byte-for-byte before this delta was
# written, and this is the same argument the estate's own author already
# uses on wp-prod-staging.
perl -0pi -e 's{^(resource "aws_route53_record" "(?:production-ns|internal-ns)" \{\n)}{$1    allow_overwrite = true # DELTA 5\n}gm' "$STAGE/main.tf"
perl -0pi -e 's{^(resource "aws_route53_record" "(?:com-ns|eu-ns)" \{\n)}{$1    allow_overwrite = true # DELTA 5\n}gm' "$STAGE/tld.tf"
NS_N="$(grep -hc 'DELTA 5' "$STAGE/main.tf" "$STAGE/tld.tf" | awk '{s+=$1} END {print s}')"
[ "$NS_N" = "4" ] || fail "DELTA 5 patched $NS_N apex NS blocks, expected 4"
log "  DELTA 5  allow_overwrite = true on the 4 apex NS blocks     (estate's own wall, see header)"

# The two copies. Everything above is shared; only the terraform block
# differs, and that difference IS the migration.
mkdir -p "$PLAIN" "$EST"
cp "$STAGE"/*.tf "$PLAIN/"
cp "$STAGE"/*.tf "$EST/"

cat > "$PLAIN/terraform.tf" <<EOF
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
      # DELTA 2: was \`version = "~> 5"\`. aws 5.x has no list resources at
      # all, which #269 records as a hard \`Unlistable marker-discovered
      # type\` for every ServerAssigned type - and all 4 zones here are
      # ServerAssigned. Pinned to the same 6.59.0 the rest of this
      # repository's artifacts are generated against, so stage 1 and
      # stages 2-5 cannot disagree about the provider either.
      version = "= 6.59.0"
    }
  }

  required_version = ">= 1.6"

  # DELTA 1: the estate's own \`cloud { organization = "datacite-ng" ... }\`
  # block is removed. This copy is the COLD one - no live block, no
  # choudoufu, plain local state, which is what stage 2 migrates from.
}
EOF

cat > "$EST/terraform.tf" <<EOF
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
      version = "= 6.59.0" # DELTA 2, see the plain copy
    }
  }

  required_version = ">= 1.6"

  # DELTA 1: cloud block out, live block in (#268).
  live {
    estate = "$ESTATE_NAME"

    # DELTA 6: aws_route53_record.allow_overwrite is a pure input - Route 53
    # has no such field, so the provider's Read never returns it. See the
    # header: without this, 14 records propose "+ allow_overwrite = true"
    # on every cold plan, forever. #275's residue store is the carrier.
    record_store "local" {
      path = ".tofu-records"
    }
  }
}
EOF
grep -q "estate = \"$ESTATE_NAME\"" "$EST/terraform.tf" || fail "DELTA 1 did not land in the estate copy"
grep -q 'record_store "local"' "$EST/terraform.tf" || fail "DELTA 6 did not write a record_store block"
grep -q 'live {' "$PLAIN/terraform.tf" && fail "the cold copy has a live block - stage 1 would not be cold"
log "  DELTA 1  cloud block removed; live block in the choudoufu copy only  (#268)"
log "  DELTA 2  aws pinned = 6.59.0 in both copies                          (#269)"
log "  DELTA 6  record_store \"local\" in the live block                      (#275)"

# ══════════════════════════════════════════════════════════════════════════
# 2. floci
# ══════════════════════════════════════════════════════════════════════════
log "=== 2. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
HEALTH=""
for _ in $(seq 1 60); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"route53"' <<< "$HEALTH" && grep -q '"ec2"' <<< "$HEALTH" && break
  sleep 2
done
grep -q '"route53"' <<< "${HEALTH:-}" || fail "floci did not come up healthy (route53) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION="$REGION"

# DELTA 4's prerequisite: a real VPC. aws_route53_zone.internal is a PRIVATE
# zone with a vpc {} block, and input.tf reads `data "aws_vpc" "datacite"`
# by the same id, so var.vpc_id has to name something that actually exists.
# Created here directly through the AWS CLI - never through choudoufu - so
# it is a genuine pre-existing account object the estate references and
# does not own.
VPC_ID="$(awsl ec2 create-vpc --cidr-block 10.90.0.0/16 --query 'Vpc.VpcId' --output text)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || fail "could not create the VPC the private zone needs"
VPC_ID_US="$(aws --endpoint-url "$ENDPOINT" --region us-east-1 ec2 create-vpc --cidr-block 10.91.0.0/16 --query 'Vpc.VpcId' --output text)"
[ -n "$VPC_ID_US" ] && [ "$VPC_ID_US" != "None" ] || fail "could not create the us-east-1 VPC"
log "  seeded a real VPC out of band: $VPC_ID (eu-west-1), $VPC_ID_US (us-east-1)"

# DELTA 4: values for the estate's twenty undefaulted variables. The real
# ones live in DataCite's TFC workspace; none is secret-bearing here
# (access_key/secret_key are the emulator's own literal "test"). Addresses
# are RFC 5737 documentation IPs; CNAME targets are example.com subdomains.
cat > "$WORK/crossing.auto.tfvars" <<EOF
access_key = "test"
secret_key = "test"

vpc_id    = "$VPC_ID"
vpc_id_us = "$VPC_ID_US"

changelog_dns_name = "changelog.example.com"
support_dns_name   = "support.example.com"
design_dns_name    = "design.example.com"
status_dns_name    = "status.example.com"

dkim_record                     = "v=DKIM1; k=rsa; p=CROSSINGDKIM"
dmarc_record                    = "v=DMARC1; p=none; rua=mailto:dmarc@example.com"
google_site_verification_record = "google-site-verification=crossing0000000000000000000000000000000"
ms_record                       = "MS=ms00000000"
verification_record             = "crossing-verification=0000000000"
dkim_salesforce                 = "v=DKIM1; k=rsa; p=CROSSINGSFDC"
dkim_alt_salesforce             = "v=DKIM1; k=rsa; p=CROSSINGSFDCALT"
dkim_cm                         = "v=DKIM1; k=rsa; p=CROSSINGCM"

siteground_ip_stage         = "192.0.2.11"
siteground_ip_prod          = "192.0.2.12"
siteground_ip_homepage_prod = "192.0.2.13"

strategy_lovable_ip_prod = "192.0.2.14"
EOF
UNSET_N="$(grep -c '^variable "' "$STAGE/var.tf")"
[ "$UNSET_N" = "22" ] || fail "var.tf declares $UNSET_N variables, expected 22 - DELTA 4 may no longer cover them"
cp "$WORK/crossing.auto.tfvars" "$PLAIN/"
cp "$WORK/crossing.auto.tfvars" "$EST/"
log "  DELTA 4  tfvars for the estate's 20 undefaulted variables  (onboarding)"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 1: COLD DEPLOY - plain terraform apply, no live block, no choudoufu
# ══════════════════════════════════════════════════════════════════════════
log ""
log "=== STAGE 1: cold deploy (stock terraform, no choudoufu anywhere) ==="
( cd "$PLAIN" && terraform init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$PLAIN" && terraform init -input=false -no-color 2>&1 | tail -30 ); fail "stage 1 init failed"; }
COLD_OUT="$(cd "$PLAIN" && terraform apply -input=false -auto-approve -no-color 2>&1)"; COLD_RC=$?
[ "$COLD_RC" -eq 0 ] || { printf '%s\n' "$COLD_OUT" | grep -E '^Error|^│' | head -40; fail "stage 1 (cold deploy) failed"; }
grep -qE "Apply complete! Resources: $INSTANCES added, 0 changed, 0 destroyed" <<< "$COLD_OUT" \
  || { grep -E 'Apply complete' <<< "$COLD_OUT"; fail "stage 1 did not create exactly $INSTANCES resources"; }
log "  $(grep -E 'Apply complete' <<< "$COLD_OUT")"
[ -f "$PLAIN/terraform.tfstate" ] || fail "stage 1 left no state file to migrate from"

# The zones, read back through the AWS CLI. `production` and `internal` are
# BOTH called datacite.org - the only thing separating them here is the
# PrivateZone flag, which is exactly why the marker is load-bearing.
zone_id_of() { # zone_id_of <dns-name.> <true|false>
  awsl route53 list-hosted-zones \
    --query "HostedZones[?Name=='$1' && Config.PrivateZone==\`$2\`].Id | [0]" \
    --output text | sed 's|/hostedzone/||'
}
PROD_ZONE="$(zone_id_of 'datacite.org.' false)"
INT_ZONE="$(zone_id_of 'datacite.org.' true)"
COM_ZONE="$(zone_id_of 'datacite.com.' false)"
EU_ZONE="$(zone_id_of 'datacite.eu.' false)"
for pair in "PROD_ZONE:$PROD_ZONE" "INT_ZONE:$INT_ZONE" "COM_ZONE:$COM_ZONE" "EU_ZONE:$EU_ZONE"; do
  [ -n "${pair#*:}" ] && [ "${pair#*:}" != "None" ] || fail "could not find ${pair%%:*} through the AWS CLI"
done
[ "$PROD_ZONE" != "$INT_ZONE" ] || fail "the public and private datacite.org zones came back with the same id"
ZONE_N="$(awsl route53 list-hosted-zones --query 'length(HostedZones)' --output text)"
[ "$ZONE_N" = "4" ] || fail "Route 53 holds $ZONE_N hosted zones after stage 1, expected 4"
log "  4 live zones: production=$PROD_ZONE internal=$INT_ZONE (both datacite.org) com=$COM_ZONE eu=$EU_ZONE"

UNMARKED=0
for Z in "$PROD_ZONE" "$INT_ZONE" "$COM_ZONE" "$EU_ZONE"; do
  A="$(awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$Z" \
       --query "ResourceTagSet.Tags[?Key=='tofu-address'].Value | [0]" --output text 2>/dev/null || echo None)"
  [ "$A" = "None" ] || UNMARKED=$((UNMARKED + 1))
done
[ "$UNMARKED" = "0" ] \
  || fail "$UNMARKED zone(s) already carry a tofu-address before migration - this crossing proves nothing"
log "  confirmed unmarked: no zone carries a tofu-address before migration"

log ""
log "STAGE 1 (cold deploy): PASS"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 2: MIGRATE - choudoufu live-import against the cold state
# ══════════════════════════════════════════════════════════════════════════
log ""
log "=== STAGE 2: choudoufu live-import ==="
( cd "$EST" && "$TOFU" init -input=false -no-color >/dev/null 2>&1 ) || {
  ( cd "$EST" && "$TOFU" init -input=false -no-color 2>&1 | tail -30 ); fail "the estate copy's init failed"; }

log "--- 2a: live-import, read-only first ---"
IMPORT_OUT="$(cd "$EST" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -no-color 2>&1)"; IMPORT_RC=$?
[ "$IMPORT_RC" -eq 0 ] || { printf '%s\n' "$IMPORT_OUT" | tail -40; fail "live-import (dry run) failed"; }
grep -qF "$TAGGABLE of $INSTANCES resource instance(s) are eligible for stamping" <<< "$IMPORT_OUT" \
  || { printf '%s\n' "$IMPORT_OUT" | tail -40
       fail "live-import did not verify exactly $TAGGABLE of $INSTANCES as eligible - the 4 zones are the only taggable objects here, all 59 record sets are UNTAGGABLE"; }
grep -qF "No tag has been written." <<< "$IMPORT_OUT" || fail "the dry run wrote a tag - it must not"
grep -qF "UNTAGGABLE ($UNTAGGABLE)" <<< "$IMPORT_OUT" \
  || { grep -E 'UNTAGGABLE' <<< "$IMPORT_OUT"; fail "expected exactly $UNTAGGABLE UNTAGGABLE instances (every aws_route53_record)"; }
log "  $TAGGABLE of $INSTANCES verified against the live system, $UNTAGGABLE correctly untaggable; nothing written yet"

log "--- 2b: -approve ---"
APPROVE_OUT="$(cd "$EST" && "$TOFU" live-import -state="$PLAIN/terraform.tfstate" -estate="$ESTATE_NAME" -approve -no-color 2>&1)"; APPROVE_RC=$?
[ "$APPROVE_RC" -eq 0 ] || { printf '%s\n' "$APPROVE_OUT" | tail -40; fail "live-import -approve failed"; }
# The counts, by exact string. #340 widened this line with two record-backed
# columns; this estate has no record-backed resource, so both read 0, and
# asserting them is how a future change that starts routing an ordinary AWS
# type through that path fails here rather than passing quietly.
grep -qF "$TAGGABLE resource(s) newly stamped, 0 already stamped, 0 newly recorded, 0 re-recorded for sensitivity only, 0 already recorded, 0 failed, $UNTAGGABLE skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve did not stamp exactly $TAGGABLE of $INSTANCES cleanly"; }
log "  $TAGGABLE stamped, $UNTAGGABLE skipped, 0 recorded, 0 failed"

# ── 2d: what #341 fixed, asserted where it is written rather than inferred ──
# An untaggable resource is still a real cloud object with real arguments,
# and allow_overwrite is one no Route 53 read can ever return. Before #341
# the migrate above wrote NOTHING for any of the 59, because ratify.go
# returned at the taggability check before building the carrier Approve's
# residue call took. The store is on disk, one file per key, so this reads
# the values it actually holds rather than trusting a log line.
RESIDUE_DIR="$EST/.tofu-records/tofu-residue/$ESTATE_NAME/aws_route53_record"
[ -d "$RESIDUE_DIR" ] \
  || fail "no residue records were written for any aws_route53_record - #341's exact symptom, and stage 3 below cannot come back empty without them"
RESIDUE_N="$(find "$RESIDUE_DIR" -type f ! -name '*.lock' | grep -c . || true)"
[ "$RESIDUE_N" = "$RESIDUE_WANT" ] \
  || { find "$RESIDUE_DIR" -type f ! -name '*.lock' | head -20
       fail "the migrate wrote $RESIDUE_N residue record(s) for aws_route53_record, expected $RESIDUE_WANT (the 4 apex NS blocks plus wp-prod-staging[0..9])"; }
# And the VALUE, not the count: every one of them says allow_overwrite = true.
# A record written with the wrong value produces an EMPTY plan that is wrong,
# which stage 3's own verdict cannot see.
TRUE_N="$(grep -l '"allow_overwrite"' "$RESIDUE_DIR"/* 2>/dev/null | xargs grep -lF '"attrValue":true' 2>/dev/null | grep -c . || true)"
[ "$TRUE_N" = "$RESIDUE_WANT" ] \
  || { cat "$RESIDUE_DIR"/* | head -5
       fail "$TRUE_N of $RESIDUE_WANT residue records hold allow_overwrite = true"; }
log "  $RESIDUE_WANT residue records written, every one of them allow_overwrite = true (#341)"
log "  and none for the other 45 record sets, which declare no residue-shaped argument"

log "--- 2c: the markers, read through the AWS CLI - never through choudoufu ---"
marker_of() { awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$1" \
  --query "ResourceTagSet.Tags[?Key=='$2'].Value | [0]" --output text 2>/dev/null || echo None; }
for pair in \
  "$PROD_ZONE:aws_route53_zone.production" \
  "$INT_ZONE:aws_route53_zone.internal" \
  "$COM_ZONE:aws_route53_zone.com" \
  "$EU_ZONE:aws_route53_zone.eu" ; do
  Z="${pair%%:*}"; WANT_ADDR="${pair#*:}"
  GOT_ADDR="$(marker_of "$Z" tofu-address)"
  [ "$GOT_ADDR" = "$WANT_ADDR" ] || fail "zone $Z carries tofu-address=$GOT_ADDR, expected $WANT_ADDR"
  GOT_EST="$(marker_of "$Z" tofu-estate)"
  [ "$GOT_EST" = "$ESTATE_NAME" ] || fail "zone $Z carries tofu-estate=$GOT_EST, expected $ESTATE_NAME"
  log "  $Z -> tofu-address=$GOT_ADDR"
done

# The markers did not displace DataCite's own Environment tag. A stamping
# pass that REPLACED the tags argument rather than merging into it would
# leave every assertion above green and quietly strip it.
PROD_ENV="$(marker_of "$PROD_ZONE" Environment)"
[ "$PROD_ENV" = "production" ] \
  || { awsl route53 list-tags-for-resource --resource-type hostedzone --resource-id "$PROD_ZONE" --output text
       fail "the production zone's own Environment tag is \"$PROD_ENV\" - the markers displaced it"; }
INT_ENV="$(marker_of "$INT_ZONE" Environment)"
[ "$INT_ENV" = "internal" ] || fail "the internal zone's own Environment tag is \"$INT_ENV\" - the markers displaced it"
log "  and DataCite's own tags survived the marker write: Environment=production / Environment=internal"

log ""
log "STAGE 2 (migrate): PASS"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 3: TEST PLAN - state deleted, live-plan empty, identities by VALUE
# ══════════════════════════════════════════════════════════════════════════
log ""
log "=== STAGE 3: no state file, live-plan, and the rendered identities ==="
rm -f "$EST/terraform.tfstate" "$EST/terraform.tfstate.backup"
[ ! -f "$EST/terraform.tfstate" ] || fail "the state file is still there"

# The trace is tens of megabytes at 63 instances, so every plan below goes
# to a file rather than into a shell variable.
plan_into() { # plan_into <outfile> [trace]
  local out="$1" tr="${2:-}"
  if [ "$tr" = "trace" ]; then
    ( cd "$EST" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color ) > "$out" 2>&1
  else
    ( cd "$EST" && "$TOFU" live-plan -input=false -no-color ) > "$out" 2>&1
  fi
}
plan_into "$WORK/plan1.log" trace; PLAN_RC=$?
[ "$PLAN_RC" -eq 0 ] || { grep -E '^Error|^│' "$WORK/plan1.log" | head -40; fail "live-plan exited $PLAN_RC"; }
[ ! -f "$EST/terraform.tfstate" ] || fail "live-plan wrote a state file"
grep -qE '^Foreign resources: (none|nothing was swept)' "$WORK/plan1.log" \
  || { grep -E '^Foreign resources:' "$WORK/plan1.log"; fail "the plan reports foreign resources"; }
log "  live-plan ran clean, nothing foreign, and no state file was written"

# The identities the run actually rendered, out of its own trace.
grep -oE 'materialized [^ ]+ from import identity "[^"]*"' "$WORK/plan1.log" \
  | sed -E 's/^materialized (.*) from import identity "(.*)"$/\1\t\2/' | sort -u > "$WORK/identities.tsv"
GOT_N="$(cut -f2 "$WORK/identities.tsv" | sort -u | grep -c .)"
[ "$GOT_N" = "$INSTANCES" ] \
  || { head -5 "$WORK/identities.tsv"; fail "the run materialized $GOT_N distinct identities, expected $INSTANCES"; }
log "  $GOT_N distinct rendered identities, one per instance - none collided"

want_identity() { # want_identity <address> <identity>
  local addr="$1" want="$2" got
  got="$(awk -F'\t' -v a="$addr" '$1==a {print $2}' "$WORK/identities.tsv")"
  [ "$got" = "$want" ] || {
    printf 'address:   %s\nrendered:  %s\nexpected:  %s\n' "$addr" "${got:-<nothing>}" "$want" >&2
    fail "$addr rendered the wrong identity"
  }
}

WANT_MX="${PROD_ZONE}_datacite.org_MX"
if [ "${BREAK:-}" = "1" ]; then
  # Not a string nothing could produce: the OTHER datacite.org zone's real
  # id, same name, same type - exactly what a derivation that resolved the
  # sibling zone reference by NAME rather than by marker would render.
  WANT_MX="${INT_ZONE}_datacite.org_MX"
  log "  BREAK=1: expecting $WANT_MX - the PRIVATE datacite.org zone's real id"
  log "           in place of the public one. Both zones exist, both are"
  log "           named datacite.org, and the plan above was empty. This"
  log "           step must fail."
fi

# The four zones: ServerAssigned, so the identity IS the live zone id, and
# it can only have come from the marker on the zone.
want_identity "aws_route53_zone.production" "$PROD_ZONE"
want_identity "aws_route53_zone.internal"   "$INT_ZONE"
want_identity "aws_route53_zone.com"        "$COM_ZONE"
want_identity "aws_route53_zone.eu"         "$EU_ZONE"
log "  4 zones: identity == the live zone id, and the two datacite.org zones did not swap"

# The sibling-attribute references (main.tf:91, main.tf:105) - the two
# sites refusal-probe's schema-less mode refuses.
want_identity "aws_route53_record.mx-datacite"  "$WANT_MX"
want_identity "aws_route53_record.txt-datacite" "${PROD_ZONE}_datacite.org_TXT"
log "  mx-datacite/txt-datacite: name = aws_route53_zone.production.name resolved to \"datacite.org\""

# The apex NS pair - same name, same type, different zone. Nothing but the
# zone half of the composite separates them.
want_identity "aws_route53_record.production-ns" "${PROD_ZONE}_datacite.org_NS"
want_identity "aws_route53_record.internal-ns"   "${INT_ZONE}_datacite.org_NS"
want_identity "aws_route53_record.com-ns"        "${COM_ZONE}_datacite.com_NS"
want_identity "aws_route53_record.eu-ns"         "${EU_ZONE}_datacite.eu_NS"
log "  4 apex NS records: production-ns and internal-ns share a name and a type and differ only by zone"

# count.index + 3, ten instances, all ten asserted individually.
for i in $(seq 0 9); do
  want_identity "aws_route53_record.wp-prod-staging[$i]" "${PROD_ZONE}_staging$((i + 3)).datacite.org_A"
done
log "  wp-prod-staging[0..9]: staging3..staging12, ten distinct identities from count.index + 3"

# The relative name floci accepts and real Route 53 would not (see header).
want_identity "aws_route53_record.lovable-verify" "${PROD_ZONE}__lovable.strategy_TXT"
log "  lovable-verify: the relative name rendered exactly as written"

if [ "${BREAK:-}" = "1" ]; then
  fail "BREAK=1: mx-datacite's real identity matched the WRONG expected value above without this script noticing - stage 3's assertion is not load-bearing"
fi


# ── 3b: the plan itself, and the residue that lets it be empty ──────────────
#
# Every identity above is right, and that was true while this crossing was
# blocked too. What was NOT true is that the plan was empty: fourteen record
# sets proposed "+ allow_overwrite = true", one line each, on every cold plan
# forever, because a migrate wrote no residue record for an untaggable
# resource at all (#341). Stage 2d asserts the fourteen records now exist and
# hold the right value; this asserts the plan they make empty.
log ""
log "--- 3b: the plan, and the residue records it reads ---"
awk '/OpenTofu will perform the following actions/,/^Plan: /' "$WORK/plan1.log" > "$WORK/plan1.diff"
CHANGED="$(grep -oE '^  # \S+ will be ' "$WORK/plan1.diff" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED" | grep -c . || true)"
[ "$N_CHANGED" = "0" ] || {
  printf 'proposed:\n%s\n' "$CHANGED" >&2
  grep -E '^      [+~-] ' "$WORK/plan1.diff" | sed -E 's/^      [+~-] ([A-Za-z0-9_]+).*/  attribute: \1/' | sort -u >&2
  fail "the plan proposes $N_CHANGED change(s); with every identity above correct and the residue records written, it must be empty"
}
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' "$WORK/plan1.log" \
  || { grep -E '^Plan: |^No changes' "$WORK/plan1.log"; fail "live-plan is not empty"; }
log "  the plan is EMPTY: 63 instances, no state file, nothing proposed"

# The residue was READ BACK, not merely written. The plan's own trace says so,
# and it must say so for the record sets and not only for the four zones -
# four fill lines was exactly the shape of the bug (#341's own evidence).
FILL_ZONES="$(grep -c 'filled .* residue attribute(s) of aws_route53_zone\.' "$WORK/plan1.log" || true)"
FILL_RECORDS="$(grep -oE 'filled [0-9]+ residue attribute\(s\) of aws_route53_record\.[^ ]+' "$WORK/plan1.log" \
  | sed -E 's/.* of //' | sort -u | grep -c . || true)"
[ "$FILL_RECORDS" = "$RESIDUE_WANT" ] || {
  grep -oE 'filled [0-9]+ residue attribute\(s\) of [^ ]+' "$WORK/plan1.log" | sort -u >&2
  fail "the plan filled residue for $FILL_RECORDS distinct aws_route53_record instance(s), expected $RESIDUE_WANT - four zones and nothing else is #341's own signature"
}
log "  and read back: $FILL_RECORDS record sets and $FILL_ZONES zones filled residue from the store"

# The estate's own text still declares allow_overwrite in exactly one place.
# If that stops being true, the "this estate would have hit #341 with no
# deltas at all" claim in the header stops being true with it.
OWN_N="$(grep -c 'allow_overwrite = true' "$SRC/main.tf")"
[ "$OWN_N" = "1" ] \
  || fail "the estate's own main.tf declares allow_overwrite in $OWN_N places, expected exactly 1 (wp-prod-staging, count = 10)"

log ""
log "STAGE 3 (test plan): PASS"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 4: TEST APPLY - apply the empty plan, assert a genuine no-op
# ══════════════════════════════════════════════════════════════════════════
log ""
log "=== STAGE 4: test apply (apply the empty plan; object counts unchanged) ==="

record_count() {
  local total=0 z n
  for z in "$PROD_ZONE" "$INT_ZONE" "$COM_ZONE" "$EU_ZONE"; do
    n="$(awsl route53 list-resource-record-sets --hosted-zone-id "$z" \
         --query 'length(ResourceRecordSets)' --output text)"
    total=$((total + n))
  done
  printf '%s\n' "$total"
}
live_record_ttl() { # live_record_ttl <zone> <name> <type>
  awsl route53 list-resource-record-sets --hosted-zone-id "$1" \
    --query "ResourceRecordSets[?Name=='$2.' && Type=='$3'].TTL | [0]" --output text
}

BEFORE_Z="$(awsl route53 list-hosted-zones --query 'length(HostedZones)' --output text)"
BEFORE_R="$(record_count)"

APPLY2_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; APPLY2_RC=$?
[ "$APPLY2_RC" -eq 0 ] || { printf '%s\n' "$APPLY2_OUT" | tail -40; fail "the post-migration apply failed"; }
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed|No changes' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete|Plan: ' <<< "$APPLY2_OUT"; fail "the post-migration apply was not a no-op"; }

AFTER_Z="$(awsl route53 list-hosted-zones --query 'length(HostedZones)' --output text)"
AFTER_R="$(record_count)"
[ "$AFTER_Z" = "$BEFORE_Z" ] || fail "hosted zone count changed across a no-op apply: $BEFORE_Z -> $AFTER_Z"
[ "$AFTER_R" = "$BEFORE_R" ] || fail "record set count changed across a no-op apply: $BEFORE_R -> $AFTER_R"
[ ! -f "$EST/terraform.tfstate" ] || fail "a state file exists after the apply"

# The markers did not move either, and the two same-named datacite.org zones
# in particular. A second stamping pass writing the wrong address onto one of
# them would leave both counts above untouched and the estate broken.
for pair in \
  "$PROD_ZONE:aws_route53_zone.production" \
  "$INT_ZONE:aws_route53_zone.internal" \
  "$COM_ZONE:aws_route53_zone.com" \
  "$EU_ZONE:aws_route53_zone.eu" ; do
  Z="${pair%%:*}"; WANT_ADDR="${pair#*:}"
  GOT_ADDR="$(marker_of "$Z" tofu-address)"
  [ "$GOT_ADDR" = "$WANT_ADDR" ] \
    || fail "after the no-op apply, zone $Z carries tofu-address=$GOT_ADDR, expected $WANT_ADDR"
done

# And the residue survived the apply. WriteBack re-classifies after every
# apply, so a bug that deleted or emptied these records here would leave the
# no-op above green and put the estate straight back to #341 on the next
# plan - which is the shape of failure this whole crossing exists to catch.
RESIDUE_AFTER="$(find "$RESIDUE_DIR" -type f ! -name '*.lock' | grep -c . || true)"
[ "$RESIDUE_AFTER" = "$RESIDUE_WANT" ] \
  || fail "the residue record count went $RESIDUE_WANT -> $RESIDUE_AFTER across a no-op apply"
log "  genuine no-op: $BEFORE_Z zones / $BEFORE_R record sets before and after, no state file,"
log "  all 4 markers unmoved, all $RESIDUE_WANT residue records intact"

log ""
log "STAGE 4 (test apply): PASS"

# ══════════════════════════════════════════════════════════════════════════
# STAGE 5: DRIFT AND RECONVERGE - mutate one UNTAGGABLE, RESIDUE-CARRYING
#                                 record set out of band
# ══════════════════════════════════════════════════════════════════════════
#
# The drift target is deliberately one of the fourteen rather than one of the
# other 45, because it is the harder case in two independent ways at once:
#
#   * it carries NO TAG, so the only way choudoufu can see the change at all
#     is by re-deriving ZONEID_NAME_TYPE from its parent zone's marker and
#     reading the record back; and
#   * its prior state is PARTLY FILLED FROM THE RECORD STORE (#341's fix). A
#     fill that overwrote a value the provider actually answered would mask
#     real drift, and this plan would come back empty. It is the one failure
#     mode a residue mechanism can introduce, and this is where it shows.
log ""
log "=== STAGE 5: drift and reconverge (one untaggable, residue-carrying record) ==="

upsert_ttl() { # upsert_ttl <zone> <name> <ttl>
  awsl route53 change-resource-record-sets --hosted-zone-id "$1" \
    --change-batch "$(printf '{"Changes":[{"Action":"UPSERT","ResourceRecordSet":{"Name":"%s","Type":"%s","TTL":%s,"ResourceRecords":[{"Value":"%s"}]}}]}' \
        "$2" "$DRIFT_RECORD_TYPE" "$3" "$DRIFT_RECORD_VALUE")" >/dev/null
}

if [ "${BREAK_STAGE5:-}" = "1" ]; then
  upsert_ttl "$PROD_ZONE" "$BREAK_RECORD_NAME" "$DRIFT_TTL" \
    || fail "the BREAK_STAGE5 second mutation did not take"
  log "  BREAK_STAGE5=1: also drifted $BREAK_RECORD_NAME's TTL - stage 5 must now"
  log "                  see TWO drifted instances and fail the single-instance check"
fi

upsert_ttl "$PROD_ZONE" "$DRIFT_RECORD_NAME" "$DRIFT_TTL" \
  || fail "the out-of-band record mutation did not take"
GOT_TTL="$(live_record_ttl "$PROD_ZONE" "$DRIFT_RECORD_NAME" "$DRIFT_RECORD_TYPE")"
[ "$GOT_TTL" = "$DRIFT_TTL" ] \
  || fail "the out-of-band mutation did not take: $DRIFT_RECORD_NAME's TTL is $GOT_TTL, expected $DRIFT_TTL"
log "  set $DRIFT_RECORD_NAME ($DRIFT_RECORD_TYPE) TTL to $DRIFT_TTL in zone $PROD_ZONE,"
log "  directly through the AWS CLI - never through choudoufu"

plan_into "$WORK/plan-drift.log"; DRIFT_RC=$?
[ "$DRIFT_RC" -eq 0 ] || { grep -E '^Error|^│' "$WORK/plan-drift.log" | head -20; fail "the drift-detection plan exited $DRIFT_RC"; }

CHANGED_ADDRS="$(grep -oE '^  # \S+ will be updated' "$WORK/plan-drift.log" | awk '{print $2}' | sort -u)"
N_DRIFTED="$(printf '%s\n' "$CHANGED_ADDRS" | grep -c . || true)"

# The address list counts UPDATES only, so a destroy or a create alongside
# the one expected update would leave it reading 1 and this stage would pass
# while the estate was rebuilt underneath it. The plan's own totals line is
# the independent check, asserted first.
PLAN_TOTALS="$(grep -oE '^Plan: [0-9]+ to add, [0-9]+ to change, [0-9]+ to destroy' "$WORK/plan-drift.log" | tail -1)"
[ -n "$PLAN_TOTALS" ] \
  || { grep -E '^Plan:|^No changes' "$WORK/plan-drift.log"; fail "the drift plan printed no 'Plan: ...' line at all - this stage's totals assertion is reading nothing"; }

if [ "${BREAK_STAGE5:-}" = "1" ]; then
  [ "$N_DRIFTED" = "1" ] \
    && fail "BREAK_STAGE5=1 set (two records drifted), but the plan proposes fixing only 1 - this assertion is not load-bearing"
  log "  BREAK_STAGE5=1: the plan proposes fixing $N_DRIFTED instances ($PLAN_TOTALS),"
  log "                  correctly more than one - the reconverge apply below is skipped"
else
  [ "$PLAN_TOTALS" = "Plan: 0 to add, 1 to change, 0 to destroy" ] \
    || { grep -E '^  # .+ will be' "$WORK/plan-drift.log"; fail "the drift plan's own totals are \"$PLAN_TOTALS\", not \"Plan: 0 to add, 1 to change, 0 to destroy\""; }
  [ "$CHANGED_ADDRS" = "$DRIFT_ADDR" ] \
    || fail "the plan proposes fixing $CHANGED_ADDRS, not $DRIFT_ADDR"
  # And the diff is the TTL, not allow_overwrite. If the residue record had
  # gone missing the plan would name that attribute here too, and the stage
  # would otherwise still pass its address assertion.
  DRIFT_ATTRS="$(awk '/OpenTofu will perform the following actions/,/^Plan: /' "$WORK/plan-drift.log" \
    | grep -E '^      [+~-] ' | sed -E 's/^      [+~-] ([A-Za-z0-9_]+).*/\1/' | sort -u)"
  [ "$DRIFT_ATTRS" = "ttl" ] \
    || { printf 'attributes in the drift diff:\n%s\n' "$DRIFT_ATTRS" >&2
         fail "the drift diff names something other than ttl - allow_overwrite here would mean the residue record is gone again"; }
  log "  the plan proposes fixing exactly one instance, $CHANGED_ADDRS, and one attribute, ttl"

  RECONVERGE_OUT="$(cd "$EST" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)"; RECONVERGE_RC=$?
  [ "$RECONVERGE_RC" -eq 0 ] || { printf '%s\n' "$RECONVERGE_OUT" | tail -40; fail "the reconverge apply failed"; }
  grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$RECONVERGE_OUT" \
    || { grep -E 'Apply complete' <<< "$RECONVERGE_OUT"; fail "the reconverge apply did not change exactly 1 resource"; }
  FIXED_TTL="$(live_record_ttl "$PROD_ZONE" "$DRIFT_RECORD_NAME" "$DRIFT_RECORD_TYPE")"
  [ "$FIXED_TTL" = "$WANT_TTL" ] \
    || fail "$DRIFT_RECORD_NAME's TTL is $FIXED_TTL after reconverging, not the configuration's $WANT_TTL"
  [ "$(record_count)" = "$AFTER_R" ] \
    || fail "the record set count is no longer $AFTER_R after reconverging"
  STILL="$(marker_of "$PROD_ZONE" tofu-address)"
  [ "$STILL" = "aws_route53_zone.production" ] \
    || fail "the parent zone's tofu-address is \"$STILL\" after the reconverge apply - the marker did not survive"
  log "  reconverged: TTL back to $WANT_TTL, $AFTER_R record sets still there, the parent"
  log "  zone's marker intact - all read through the AWS CLI"
fi

log ""
log "STAGE 5 (drift and reconverge): PASS"

log ""
log "=== PASS: all five stages, against DataCite's own global DNS estate ==="
log ""
log "4 hosted zones - two of them BOTH named datacite.org, one public and one"
log "private - and 59 Route 53 record sets that can carry no tag at all."
log ""
log "  1. 63 of 63 cold-deployed by stock Terraform, nothing marked."
log "  2. 4 stamped, 59 correctly UNTAGGABLE, and $RESIDUE_WANT residue records written"
log "     for untaggable record sets - which is what #341 fixed."
log "  3. State file deleted, replanned EMPTY from the markers alone, with all"
log "     63 rendered identities asserted by value against Route 53's own"
log "     answer: the two same-named zones did not swap, the ten"
log "     count.index + 3 instances are staging3..staging12, and the two"
log "     records whose name comes from a sibling zone's own name resolved."
log "  4. A genuine no-op apply."
log "  5. One untaggable, residue-carrying record set drifted out of band and"
log "     reconverged to exactly that object and nothing else."
log ""
log "Run again with BREAK=1 (stage 3's identity assertion) or BREAK_STAGE5=1"
log "(stage 5's single-instance assertion) to see each go red."
