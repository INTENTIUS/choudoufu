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
#                     all 63 rendered identity strings against the AWS
#                     CLI's own answer - AND PIN THE ONE THING THAT KEEPS
#                     THE PLAN FROM BEING EMPTY. FAILS (blocked), see below.
#   4. TEST APPLY     NOT RUN - there is no empty plan to apply.
#   5. DRIFT AND      NOT RUN - needs a converged estate to drift from.
#      RECONVERGE
#
# So this crossing is 2 of 5, with stage 3 producing every identity
# correctly and one real, general, previously-unrecorded choudoufu defect.
# The script exits 0 when it reaches EXACTLY that blocker and non-zero on
# anything else, including the plan coming back empty - which is what
# happens the day the defect is fixed, and is the signal to promote this
# script to the full five stages. Same convention as
# corpus-crossref-orcid-agent and corpus-cncf-k8s-infra-aws-capa-ami:
# prove the gap, never fabricate the crossing.
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
# THE SECOND WALL THIS CROSSING FOUND (stage 3, and it IS choudoufu's -
# already-built machinery that the onboarding edit has to switch on):
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
#   HOW GENERAL: 342 of the 1025 types in
#   internal/live/identity/table_generated.go are untaggable by
#   live/survey-full.json's own taggable signal (683 taggable), and every
#   one of them is excluded from migrate-time residue recording the same
#   way. How many of the 342 actually carry a residue-shaped argument is a
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
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4731, clear of every
#                other live/e2e fixture's port).
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#   BREAK        set to 1 to corrupt one expected identity string ahead of
#                stage 3's assertion AND tamper a second live record ahead
#                of stage 5's, proving both are load-bearing rather than
#                greps that always match.
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
BLOCKS=54
INSTANCES=63
TAGGABLE=4
UNTAGGABLE=59

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
grep -qF "$TAGGABLE resource(s) newly stamped, 0 already stamped, 0 failed, $UNTAGGABLE skipped" <<< "$APPROVE_OUT" \
  || { printf '%s\n' "$APPROVE_OUT" | tail -20; fail "live-import -approve did not stamp exactly $TAGGABLE of $INSTANCES cleanly"; }
log "  $TAGGABLE stamped"

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

# ── the blocker, pinned exactly ─────────────────────────────────────────────
#
# Every identity above is right. The plan is still not empty, and this is
# the one reason why. See the header's SECOND WALL section: the residue
# store never learns anything about an UNTAGGABLE resource, because
# live-import's Approve skips recordResidueFor for exactly the entries
# Ratify returned no *eligible for - and ratify.go returns early, before
# the ReadResource that builds one, the moment taggable(schema.Block) is
# false. So all 59 record sets migrate with no residue record, and
# allow_overwrite - a Route 53 non-field the provider's Read can never
# return - reads null on every cold plan.
#
# This block asserts the blocker is EXACTLY what it was when measured, and
# nothing more. It fails if the diff grows, if it names any other
# attribute, if it touches any other address - and it fails if the plan
# comes back EMPTY, which is what happens the day the fix lands and is the
# signal to promote this script to the full five stages.
log ""
log "--- 3b: the blocker, pinned ---"
awk '/OpenTofu will perform the following actions/,/^Plan: /' "$WORK/plan1.log" > "$WORK/plan1.diff"
CHANGED="$(grep -oE '^  # \S+ will be updated' "$WORK/plan1.diff" | awk '{print $2}' | sort -u)"
N_CHANGED="$(printf '%s\n' "$CHANGED" | grep -c . || true)"
ATTRS="$(grep -E '^      [+~-] ' "$WORK/plan1.diff" | sed -E 's/^      [+~-] ([A-Za-z0-9_]+).*/\1/' | sort -u)"

EXPECTED_CHANGED="$(printf '%s\n' \
  aws_route53_record.com-ns \
  aws_route53_record.eu-ns \
  aws_route53_record.internal-ns \
  aws_route53_record.production-ns \
  aws_route53_record.wp-prod-staging'[0]' \
  aws_route53_record.wp-prod-staging'[1]' \
  aws_route53_record.wp-prod-staging'[2]' \
  aws_route53_record.wp-prod-staging'[3]' \
  aws_route53_record.wp-prod-staging'[4]' \
  aws_route53_record.wp-prod-staging'[5]' \
  aws_route53_record.wp-prod-staging'[6]' \
  aws_route53_record.wp-prod-staging'[7]' \
  aws_route53_record.wp-prod-staging'[8]' \
  aws_route53_record.wp-prod-staging'[9]' | sort -u)"

[ "$N_CHANGED" != "0" ] || {
  log "  The plan came back EMPTY."
  log ""
  log "  That is not a failure of this estate - it means the residue gap this"
  log "  script pins has been FIXED. Promote this crossing to the full five"
  log "  stages: restore stage 3's empty-plan assertion and re-enable stages"
  log "  4 and 5 below, then re-run and record the real result."
  fail "the pinned blocker no longer reproduces - see the message above"
}
[ "$CHANGED" = "$EXPECTED_CHANGED" ] || {
  printf 'proposed:\n%s\n\nexpected:\n%s\n' "$CHANGED" "$EXPECTED_CHANGED" >&2
  fail "the plan proposes a different set of objects than the pinned blocker - something else is wrong now"
}
[ "$ATTRS" = "allow_overwrite" ] || {
  printf 'attributes in the diff:\n%s\n' "$ATTRS" >&2
  fail "the diff names an attribute other than allow_overwrite - this is no longer only the pinned blocker"
}
grep -qE '^Plan: 0 to add, 14 to change, 0 to destroy\.' "$WORK/plan1.diff" \
  || { grep -E '^Plan: ' "$WORK/plan1.diff"; fail "the plan is not 0/14/0"; }

# Ten of the fourteen carry allow_overwrite in DataCite's own published
# text. If that stops being true the header's "the estate would have hit
# this with no deltas at all" claim stops being true with it.
OWN_N="$(grep -c 'allow_overwrite = true' "$SRC/main.tf")"
[ "$OWN_N" = "1" ] \
  || fail "the estate's own main.tf declares allow_overwrite in $OWN_N places, expected exactly 1 (wp-prod-staging, count = 10)"

log "  0 to add, 14 to change, 0 to destroy - and the whole diff is one line each:"
log "    + allow_overwrite = true"
log "  on exactly the 4 apex NS blocks (DELTA 5's) and wp-prod-staging[0..9]"
log "  (the estate's own, count = 10). No other address, no other attribute."
log "  Every one of the 63 rendered identities above is correct; the residue"
log "  store simply has no record for any of the 59 untaggable record sets,"
log "  because live-import never builds one for an untaggable resource."

log ""
log "STAGE 3 (test plan): FAIL - BLOCKED, pinned above"


# ══════════════════════════════════════════════════════════════════════════
# STAGES 4 AND 5: NOT RUN, and why
# ══════════════════════════════════════════════════════════════════════════
#
# Stage 4 is "apply the empty plan and assert a genuine no-op". There is no
# empty plan to apply: the pinned blocker makes stage 3's plan 0/14/0.
# Applying it would write allow_overwrite into fourteen live record sets and
# still not settle - the next cold plan proposes the identical fourteen,
# because nothing about the apply teaches the record store anything for an
# untaggable resource. Stage 5 is drift-and-reconverge, which needs a
# converged estate to drift FROM.
#
# So both are honestly not_run rather than quietly skipped. The code for
# both is written and reviewed against this estate - a full before/after
# listing of every zone and record set for stage 4's no-op, and a TTL
# mutation on support.datacite.org for stage 5 - and lives in this file's
# git history at the commit that first pinned this blocker. Restore it when
# the residue gap closes; the message stage 3 prints on an empty plan says
# so too.
log ""
log "STAGE 4 (test apply):        not run - stage 3 has no empty plan to apply"
log "STAGE 5 (drift/reconverge):  not run - needs a converged estate to drift from"

log ""
log "=== PASS: the pinned blocker reproduces exactly, and nothing else does ==="
log ""
log "DataCite's own global DNS root module - 4 hosted zones, two of them"
log "both named datacite.org, and 59 Route 53 record sets that can carry no"
log "tag at all - cold-deployed 63 of 63 under stock Terraform, migrated"
log "with 4 markers, and replanned from those markers with the state file"
log "deleted."
log ""
log "All 63 rendered identities are correct and distinct, asserted by value"
log "against Route 53's own answer: the two same-named zones did not swap,"
log "the ten count.index + 3 instances are staging3..staging12, and the two"
log "records whose name comes from a sibling zone's own name resolved to"
log "\"datacite.org\"."
log ""
log "The plan is still not empty. 14 record sets propose one line each,"
log "+ allow_overwrite = true, because live-import builds no residue record"
log "for an untaggable resource - ratify.go returns before the ReadResource"
log "that would build one, the moment taggable(schema.Block) is false. Ten"
log "of the fourteen carry that argument in DataCite's own published text."
log ""
log "Run again with BREAK=1: everything through stage 2 still passes, and"
log "stage 3's identity assertion goes red on the private datacite.org"
log "zone's id standing in for the public one."
