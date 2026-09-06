#!/usr/bin/env bash
# (moved from the justfile's retired demo-record-located recipe; run with: just demo-run record-located)
# Issue #270's record-located class end to end: an object with nowhere to
# carry an ownership marker, whose id the provider minted at create time,
# found again after the state file is deleted - by the estate's record store
# and by nothing else. aws_cloudfront_public_key's id appears nowhere in the
# configuration, so the run's rendered identity is checked against the
# EMULATOR's own answer rather than against the record it read; the run then
# points one record at the other key's object and requires that check to
# fail. Ends by deleting a record and proving a lost one costs an announced
# duplicate, never a deletion. Needs Docker and the AWS CLI; runs on its own
# port (4605) so it can run beside `just demo`.
set -euo pipefail

# identity.ClassRecordLocated, end to end against a real emulator.
#
# The claim under test: an object choudoufu created, which has nowhere to
# carry an ownership marker and whose identity the provider minted at create
# time, is found again after the state file is deleted - by the estate's
# record store and by nothing else - and the identity it is found by is the
# identity of the RIGHT live object.
#
# Why this exists. Issue #270's whole mechanism landed with unit coverage
# (internal/live/identity/located_test.go,
# internal/live/projection/located_test.go) and none of it had ever touched
# a cloud. Every piece could be individually correct and the crossing still
# fail: the store could be written after the state the plan reads, the
# import id could be an attribute the provider does not round-trip, the
# located namespace could be swept into the destroy path.
#
# What this fixture can prove that a unit test cannot:
#
#   1. the id choudoufu records is the id the provider accepts back as an
#      import identity, against a real API surface;
#   2. the instance binds to a live object by that string alone, with no
#      state file, no tag, and nothing in the configuration that could
#      produce it - aws_cloudfront_public_key's id is server-minted and
#      appears nowhere in main.tf;
#   3. a LOST record proposes a create and destroys nothing, which is the
#      trade issue #270 states: an announced duplicate, never a silent
#      deletion.
#
# And what makes it a measurement rather than an exercise: step 7 does not
# compare the run against the record. That comparison is vacuous - a record
# pointing at the wrong object would agree with itself perfectly. It
# compares the run against THE EMULATOR: step 3 asks CloudFront which public
# key id carries the name "rl-e2e-alpha", and step 7 requires the run to
# have rendered that id for
# aws_cloudfront_public_key.signers["rl-e2e-alpha"]. Step 8 then breaks
# exactly that, by pointing one record at the other key's live object, and
# requires the check to fail - because a harness that passes when the
# identity is wrong is worse than no harness.
#
# Both halves were mutation-checked when this landed. Making step 7 expect
# the wrong id fails the run at step 7 WITH THE PLAN STILL EMPTY, which is
# the whole reason the value is asserted rather than the verdict; and
# neutering step 8's mutation fails the run at step 8, so step 8 cannot pass
# by not having broken anything.
#
#   bash live/e2e/record-located/run.sh
#
# Env overrides:
#   TOFU_BIN     path to a prebuilt choudoufu binary; skips the `go build`.
#   FLOCI_PORT   host port for the emulator (default 4608, clear of run.sh's
#                4566, dataread-projection's 4599, tagging-sweep's 4601,
#                create-over's 4602 and per-element's 4604 - moved off 4605
#                itself in #520, which had been shared with corpus-crossing
#                and so could never run alongside it - so every harness can
#                run at once). Note this default only matters for a
#                hand-invoked run: `go run ./tools/gauntlet run` always
#                assigns FLOCI_PORT itself (#520) and never falls back to
#                this value.
#   FLOCI_IMAGE  the emulator image; defaults to the digest pin in
#                live/floci-image.
#
# Exit codes: 0 on a real pass, non-zero on a real failure. Every assertion
# reads actual command output, an exit code, a file on disk, or the
# emulator's own answer through the AWS CLI - never a timeout.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FIXTURE="$ROOT/live/e2e/record-located"
WORK="$(mktemp -d)"
FLOCI_PORT="${FLOCI_PORT:-4608}"
FLOCI_NAME="choudoufu-record-located-$$"
FLOCI_IMAGE="${FLOCI_IMAGE:-$(cat "$ROOT/live/floci-image")}"
ENDPOINT="http://127.0.0.1:${FLOCI_PORT}"

ESTATE="record-located-e2e"

cleanup() {
  docker rm -f "$FLOCI_NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

log() { printf '%s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

awsl() { aws --endpoint-url "$ENDPOINT" --region us-east-1 "$@"; }

# strip_trace drops the TF_LOG lines so a failure message is readable. The
# trace itself is what steps 7 and 8 read, so it is only stripped for
# printing.
strip_trace() { grep -vE '^[0-9]{4}-[0-9]{2}-[0-9]{2}T' || true; }

# ── 0. tools ────────────────────────────────────────────────────────────────
log "=== 0. tools ==="
command -v docker >/dev/null 2>&1 || fail "docker is not on PATH"
docker info >/dev/null 2>&1 || fail "docker is not running"
command -v aws >/dev/null 2>&1 || fail "the AWS CLI is not on PATH"
command -v python3 >/dev/null 2>&1 || fail "python3 is not on PATH"

if [ -n "${TOFU_BIN:-}" ]; then
  TOFU="$TOFU_BIN"
  [ -x "$TOFU" ] || fail "TOFU_BIN=$TOFU_BIN is not an executable file"
  log "  using TOFU_BIN=$TOFU"
else
  mkdir -p "$WORK/bin"
  TOFU="$WORK/bin/choudoufu"
  ( cd "$ROOT" && go build -o "$TOFU" ./cmd/choudoufu ) || fail "go build ./cmd/choudoufu failed"
  log "  built $TOFU from $ROOT"
fi

# ── 1. floci ────────────────────────────────────────────────────────────────
log "=== 1. floci on :$FLOCI_PORT ($FLOCI_IMAGE) ==="
docker run -d --rm -p "${FLOCI_PORT}:4566" --name "$FLOCI_NAME" "$FLOCI_IMAGE" >/dev/null \
  || fail "docker run for $FLOCI_NAME failed"
for _ in $(seq 1 45); do
  HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
  grep -q '"cloudfront"' <<< "$HEALTH" && grep -q '"ecr"' <<< "$HEALTH" && break
  sleep 2
done
HEALTH="$(curl -fs "${ENDPOINT}/_localstack/health" 2>/dev/null)" || true
grep -q '"cloudfront"' <<< "$HEALTH" || fail "floci did not come up healthy (cloudfront) at $ENDPOINT"
grep -q '"ecr"' <<< "$HEALTH" || fail "floci did not come up healthy (ecr) at $ENDPOINT"
log "  healthy"

export AWS_ENDPOINT_URL="$ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

MAIN="$WORK/estate"
mkdir -p "$MAIN"
cp "$FIXTURE/main.tf" "$MAIN/main.tf"
RECORDS="$MAIN/.tofu-records"
# GitHub issue #364 unit A1 folded the once-separate "tofu-located"
# namespace into the single "tofu-records" root every record now lives
# under (internal/live/projection/record.go's RecordKeyPrefix/RecordKey);
# what used to be a directory a located key could never share with a
# record-backed one is now the envelope's own "kind" field - see step 4
# below.
LOCATED="$RECORDS/tofu-records/$ESTATE"

# located_key reproduces internal/live/projection's RecordKey encoding for
# one instance address: the base64url of the address string, unpadded. It is
# spelled out rather than discovered by listing the directory on purpose -
# a step that globbed would still pass if the addresses were wrong.
located_key() { printf '%s' "$1" | base64 | tr -d '=\n' | tr '+/' '-_'; }

ALPHA_ADDR='aws_cloudfront_public_key.signers["rl-e2e-alpha"]'
BRAVO_ADDR='aws_cloudfront_public_key.signers["rl-e2e-bravo"]'
POLICY_ADDR='aws_ecr_registry_policy.registry'
VPC_ADDR='aws_vpc.control'

ALPHA_REC="$LOCATED/aws_cloudfront_public_key/$(located_key "$ALPHA_ADDR")"
BRAVO_REC="$LOCATED/aws_cloudfront_public_key/$(located_key "$BRAVO_ADDR")"
POLICY_REC="$LOCATED/aws_ecr_registry_policy/$(located_key "$POLICY_ADDR")"
# GitHub issue #364 unit A2's foundation-order ruling (writeback.go's
# writeBackRecordEnvelopes): every recordable instance now gets its
# identity recorded best-effort, not only a located route's - so the
# taggable, marker-tracked aws_vpc.control gets a kind=identity record
# alongside the three genuinely located ones, even though its marker
# remains its real ownership carrier. See step 4 below.
VPC_REC="$LOCATED/aws_vpc/$(located_key "$VPC_ADDR")"

# record_id reads identity.import_id out of one located record file's
# merged envelope (record.go's identityPayload; "importID" was the retired
# locatedPayload's field name before GitHub issue #364 unit A1).
record_id() {
  [ -f "$1" ] || fail "no located record at $1"
  python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["identity"]["import_id"])' "$1"
}

# kind_of reads the merged envelope's own "kind" field - recordKindObject
# is the only kind builder.discoverOrphanedRecords ever proposes destroying
# for; recordKindIdentity is what a located identity, having no cloud
# object the record authorizes, always carries (record.go's own comment on
# recordKindIdentity).
kind_of() {
  [ -f "$1" ] || fail "no located record at $1"
  python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["kind"])' "$1"
}

# ── 2. stand the estate up ──────────────────────────────────────────────────
# 2 public keys + 1 registry policy + 1 VPC. Asserted exactly: a fixture that
# quietly stopped creating one of them would still pass every plan assertion
# below, because there would be nothing left to disagree about.
log "=== 2. apply: 2 public keys, 1 registry policy, 1 VPC ==="
( cd "$MAIN" && "$TOFU" init -input=false -no-color >/dev/null ) || fail "init failed"
APPLY_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  strip_trace <<< "$APPLY_OUT" | tail -40
  fail "the apply failed"
}
grep -qE 'Apply complete! Resources: 4 added' <<< "$APPLY_OUT" \
  || { strip_trace <<< "$APPLY_OUT" | tail -20; fail "the apply did not create exactly 4 resources"; }
log "  $(grep -E 'Apply complete' <<< "$APPLY_OUT")"

# ── 3. the emulator's own answer, which is what step 7 measures against ─────
# Read through the AWS CLI, never through choudoufu. The point of this block
# is that it is an EXTERNAL source: comparing the run's rendered identity
# against the record it read would be comparing the mechanism with itself.
log "=== 3. the emulator's answer: which live object carries which name ==="
LIVE_IDS="$(awsl cloudfront list-public-keys --query 'PublicKeyList.Items[].Id' --output text | tr '\t' '\n' | grep -v '^$' || true)"
[ "$(wc -l <<< "$LIVE_IDS" | tr -d ' ')" = "2" ] \
  || fail "expected exactly 2 live CloudFront public keys, got: $(tr '\n' ' ' <<< "$LIVE_IDS")"

ALPHA_LIVE=""; BRAVO_LIVE=""
while read -r kid; do
  [ -n "$kid" ] || continue
  kname="$(awsl cloudfront get-public-key --id "$kid" --query 'PublicKey.PublicKeyConfig.Name' --output text)"
  case "$kname" in
    rl-e2e-alpha) ALPHA_LIVE="$kid" ;;
    rl-e2e-bravo) BRAVO_LIVE="$kid" ;;
    *) fail "a live public key $kid carries an unexpected name $kname" ;;
  esac
done <<< "$LIVE_IDS"
[ -n "$ALPHA_LIVE" ] || fail "no live public key is named rl-e2e-alpha"
[ -n "$BRAVO_LIVE" ] || fail "no live public key is named rl-e2e-bravo"
[ "$ALPHA_LIVE" != "$BRAVO_LIVE" ] || fail "both names resolved to the same live id $ALPHA_LIVE"
log "  rl-e2e-alpha is $ALPHA_LIVE"
log "  rl-e2e-bravo is $BRAVO_LIVE"

POLICY_LIVE="$(awsl ecr get-registry-policy --query 'registryId' --output text)"
[ -n "$POLICY_LIVE" ] && [ "$POLICY_LIVE" != "None" ] || fail "the registry policy is not live on the emulator"
log "  the registry policy is on registry $POLICY_LIVE"

# The identities are server-minted, and this is what says so. If a public
# key id appeared in the configuration, the run could re-derive it and the
# record would be proving nothing.
for live in "$ALPHA_LIVE" "$BRAVO_LIVE"; do
  grep -qF "$live" "$MAIN/main.tf" \
    && fail "the live id $live appears in main.tf, so it is derivable from the configuration and the record store is not what recovers it"
done
log "  neither id appears in main.tf: nothing but the record can supply them"

# ── 4. what the store holds, and what it does NOT hold ──────────────────────
log "=== 4. the record store ==="
[ -f "$ALPHA_REC" ]  || fail "no located record for $ALPHA_ADDR at $ALPHA_REC"
[ -f "$BRAVO_REC" ]  || fail "no located record for $BRAVO_ADDR at $BRAVO_REC"
[ -f "$POLICY_REC" ] || fail "no located record for $POLICY_ADDR at $POLICY_REC"

[ "$(record_id "$ALPHA_REC")" = "$ALPHA_LIVE" ] \
  || fail "the record for $ALPHA_ADDR holds $(record_id "$ALPHA_REC"), but the emulator says rl-e2e-alpha is $ALPHA_LIVE"
[ "$(record_id "$BRAVO_REC")" = "$BRAVO_LIVE" ] \
  || fail "the record for $BRAVO_ADDR holds $(record_id "$BRAVO_REC"), but the emulator says rl-e2e-bravo is $BRAVO_LIVE"
[ "$(record_id "$POLICY_REC")" = "$POLICY_LIVE" ] \
  || fail "the record for $POLICY_ADDR holds $(record_id "$POLICY_REC"), but the emulator says the registry is $POLICY_LIVE"
log "  3 located records, each holding the id the emulator agrees with"

# The namespace split, observed rather than assumed. GitHub issue #364
# unit A1 merged the once-separate tofu-records/tofu-located roots into
# one per-instance envelope (internal/live/projection/record.go), so
# "which directory is this key under" is no longer a question that can be
# asked - what used to be answered by a directory is now answered by the
# envelope's own "kind" field. recordKindObject is the only kind
# builder.discoverOrphanedRecords ever proposes destroying for; a located
# key must carry recordKindIdentity, because for a record-BACKED resource
# the record IS the object (destroying it is correct) but for a located
# one it would drive a cloud deletion from a stale file. See record.go's
# own comment on recordKindIdentity.
> "$WORK/record-ns"
count_ns() { find "$RECORDS/$1" -type f ! -name '*.lock' ! -name '*.tmp-*' ! -name '.store-sentinel' 2>/dev/null > "$WORK/record-ns" || true
             wc -l < "$WORK/record-ns" | tr -d ' '; }
# 4, not 3: GitHub issue #364 unit A2's ruling means aws_vpc.control gets
# an identity record too now, alongside the three genuinely located
# instances - see VPC_REC's own comment above. That is fine precisely
# because the count that matters is not "how many files" but "how many
# carry kind=object", checked below as zero.
RECORD_NS_FILES="$(count_ns tofu-records)"
[ "$RECORD_NS_FILES" = "4" ] \
  || fail "expected 4 files under the merged tofu-records/ tree, found $RECORD_NS_FILES"
for rec in "$ALPHA_REC" "$BRAVO_REC" "$POLICY_REC" "$VPC_REC"; do
  [ "$(kind_of "$rec")" = "identity" ] \
    || fail "$rec carries kind=$(kind_of "$rec"), not identity - a located identity has become delete authority"
done
log "  4 keys under the merged tofu-records/ tree, each carrying kind=identity: nothing here is delete authority"

# ── 5. delete the state file, and find the objects again ────────────────────
log "=== 5. delete the state file ==="
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
[ ! -f "$MAIN/terraform.tfstate" ] || fail "the state file is still there"
log "  gone; the record store is now the only thing that knows which key is which"

log "=== 6. live-plan, from the record alone ==="
set +e
PLAN_OUT="$(cd "$MAIN" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
set -e
[ "$PLAN_RC" -eq 0 ] || { strip_trace <<< "$PLAN_OUT" | tail -40; fail "live-plan exited $PLAN_RC"; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT" \
  || { strip_trace <<< "$PLAN_OUT" | grep -vE '^\s*$' | tail -40
       fail "the plan is not empty. If a public key is proposed for creation, its located record did not name the live object - read step 7's strings before assuming which half is wrong."; }
# A sweep that HAPPENED, not an absent one. aws_vpc is in the fixture
# precisely so this line reports a real enumeration: with only client-named
# and located types the run sweeps nothing at all, and "no located object was
# proposed for destruction" would then be true of a run that looked at
# nothing.
grep -qE '^Foreign resources: none among the [0-9]+ types? swept' <<< "$PLAN_OUT" \
  || { grep -E '^Foreign resources:' <<< "$PLAN_OUT"
       fail "the foreign sweep did not report a completed sweep with nothing foreign in it. Either a live object this estate owns was read as unowned, or no sweep ran - and the second makes every negative claim below vacuous."; }
log "  nothing to create, nothing to destroy, and the sweep found nothing foreign"

# ── 7. THE VALUE, not the verdict ───────────────────────────────────────────
# An empty plan says the instances bound to something. It does not say WHAT.
# Both public keys exist, both ids are well-formed UUIDs, and either one
# imports cleanly - so a swapped pair round-trips through the provider
# without complaint at the point of import. The rendered string is the only
# place the binding is observable, and step 3's CLI reads are the only
# external thing that can say which string is right.
log "=== 7. the rendered identities, read out of the run and checked against the emulator ==="

# rendered_identity prints the import identity the run materialized addr
# from, or nothing. Non-fatal by design: step 8 needs to call it and get a
# WRONG answer back.
#
# The matching is LITERAL, through awk's index() rather than a regex,
# because an instance address carries [, ] and " - a for_each key is
# arbitrary text and escaping it into a pattern is the kind of thing that
# works until a fixture changes its keys.
rendered_identity() {
  awk -v pfx="materialized $1 from import identity \"" '
    {
      i = index($0, pfx)
      if (i == 0) next
      rest = substr($0, i + length(pfx))
      j = index(rest, "\"")
      if (j > 0) print substr(rest, 1, j - 1)
    }
  ' <<< "$PLAN_OUT" | tail -1
}

want_identity() {
  local addr="$1" want="$2" got
  got="$(rendered_identity "$addr")"
  [ -n "$got" ] || { grep -oE 'materialized [^ ]+ from import identity "[^"]*"' <<< "$PLAN_OUT" | sort -u
                     fail "$addr was never materialized from an import identity. The identities the run did render are listed above."; }
  [ "$got" = "$want" ] \
    || fail "$addr materialized from import identity \"$got\", but the emulator says the object it should own is \"$want\". The plan was EMPTY while this was wrong: both objects exist and either id imports cleanly, so nothing on the wire would have objected. See internal/live/projection/located.go."
  log "    $addr -> $got"
}

want_identity "$ALPHA_ADDR"  "$ALPHA_LIVE"
want_identity "$BRAVO_ADDR"  "$BRAVO_LIVE"
want_identity "$POLICY_ADDR" "$POLICY_LIVE"
log "  every located instance bound to the live object the emulator names"

# ── 8. the check is not vacuous ─────────────────────────────────────────────
# Break the identity and require the check to fail. One field in one file:
# bravo's record is pointed at ALPHA's live object, with its "address" field
# left correct - so RecordStore.getRaw's key/payload cross-check still passes
# and the only thing wrong is which object the id names.
log "=== 8. break the identity, and require step 7's check to catch it ==="
cp "$BRAVO_REC" "$WORK/bravo.record.bak"
python3 - "$BRAVO_REC" "$ALPHA_LIVE" <<'PY'
import json, sys
p, wrong = sys.argv[1], sys.argv[2]
rec = json.load(open(p))
rec["identity"]["import_id"] = wrong
open(p, "w").write(json.dumps(rec, separators=(",", ":")))
PY
[ "$(record_id "$BRAVO_REC")" = "$ALPHA_LIVE" ] || fail "the mutation did not take"

rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
PLAN_OUT="$(cd "$MAIN" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"
set -e
BROKEN="$(rendered_identity "$BRAVO_ADDR")"
[ "$BROKEN" = "$ALPHA_LIVE" ] \
  || fail "after pointing bravo's record at $ALPHA_LIVE the run rendered \"$BROKEN\" for $BRAVO_ADDR. The identity did not come from the record, so step 7 is measuring something else."
[ "$BROKEN" != "$BRAVO_LIVE" ] \
  || fail "step 7's assertion is vacuous: the run rendered bravo's own id even with the record pointing elsewhere"
log "  with the record broken, the run renders $BROKEN - which is alpha's object, not bravo's"
log "  step 7's want_identity would have failed here, which is what makes it a measurement"

# What the run itself does with two instances resolved to ONE live object,
# pinned as an OBSERVATION rather than asserted as correct. Today it does
# not refuse: both instances materialize from the same id and the plan
# proposes replacing the shared object, which would destroy the object the
# OTHER instance owns. Nothing in this class notices. If this assertion ever
# goes red, a refusal has been added and that is the fix landing - invert it
# and delete this paragraph.
DUP_ALPHA="$(rendered_identity "$ALPHA_ADDR")"
[ "$DUP_ALPHA" = "$ALPHA_LIVE" ] \
  || fail "with bravo's record broken, alpha rendered \"$DUP_ALPHA\" rather than its own id; the observation below no longer describes what happens"
log "  observed, not endorsed: both instances resolved to $ALPHA_LIVE and the run did not refuse."
log "  A wrong located record is invisible to every verdict-level check; only step 7 sees it."

cp "$WORK/bravo.record.bak" "$BRAVO_REC"
[ "$(record_id "$BRAVO_REC")" = "$BRAVO_LIVE" ] || fail "restoring bravo's record failed"
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
PLAN_OUT="$(cd "$MAIN" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"
PLAN_RC=$?
set -e
[ "$PLAN_RC" -eq 0 ] || { strip_trace <<< "$PLAN_OUT" | tail -30; fail "live-plan exited $PLAN_RC after the record was restored"; }
grep -qE 'No changes|Plan: 0 to add, 0 to change, 0 to destroy' <<< "$PLAN_OUT" \
  || { strip_trace <<< "$PLAN_OUT" | grep -vE '^\s*$' | tail -30; fail "the plan is not empty again after restoring bravo's record"; }
want_identity "$BRAVO_ADDR" "$BRAVO_LIVE"
log "  record restored, plan empty again"

# ── 9. and it converges ─────────────────────────────────────────────────────
# One empty plan is not evidence. Issue #266's defect produced one new
# resource per run because every run started where the last one did, and an
# empty plan is a proposal rather than an outcome - this applies it.
log "=== 9. the next run adds nothing and removes nothing ==="
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
APPLY2_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  strip_trace <<< "$APPLY2_OUT" | tail -30; fail "the second apply failed"; }
grep -qE 'Apply complete! Resources: 0 added, 0 changed, 0 destroyed' <<< "$APPLY2_OUT" \
  || { grep -E 'Apply complete|Plan: ' <<< "$APPLY2_OUT"
       fail "the second apply was not a no-op. Anything added is issue #266's shape on the located class; anything destroyed is worse."; }

AFTER="$(awsl cloudfront list-public-keys --query 'PublicKeyList.Items[].Id' --output text | tr '\t' '\n' | grep -c . || true)"
[ "$AFTER" = "2" ] || fail "there are now $AFTER live public keys, not 2 - the second run changed the cloud"
[ "$(record_id "$ALPHA_REC")" = "$ALPHA_LIVE" ] || fail "alpha's record changed across the second run"
[ "$(record_id "$BRAVO_REC")" = "$BRAVO_LIVE" ] || fail "bravo's record changed across the second run"
log "  converged: 0 added, 0 destroyed, still 2 live keys, records unchanged"

# ── 10. a LOST record risks a duplicate, never a deletion ───────────────────
# The other half of the design, and the one that decides whether this is
# safe. materializeLocated's "not found" is deliberately the same answer for
# "never created" and "record lost": the instance reads unbound and a create
# is proposed. What must NOT happen is the live object being read as an
# orphan and proposed for destruction, because a lost note about where
# something is is not permission to delete it.
log "=== 10. delete alpha's record: a create is proposed, nothing is destroyed ==="
rm -f "$ALPHA_REC"
[ ! -f "$ALPHA_REC" ] || fail "alpha's record is still there"
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
LOST_OUT="$(cd "$MAIN" && TF_LOG=trace "$TOFU" live-plan -input=false -no-color 2>&1)"
LOST_RC=$?
set -e
[ "$LOST_RC" -eq 0 ] || { strip_trace <<< "$LOST_OUT" | tail -40; fail "live-plan exited $LOST_RC with a record missing"; }

grep -qE '^Plan: 1 to add, 0 to change, 0 to destroy\.' <<< "$LOST_OUT" \
  || { grep -E '^Plan: ' <<< "$LOST_OUT"
       fail "a lost located record must propose exactly one create and zero destroys. A destroy here means a missing note about where an object is was read as permission to delete it, which is the failure issue #270 exists to prevent."; }
grep -qF "# $ALPHA_ADDR will be created" <<< "$LOST_OUT" \
  || { strip_trace <<< "$LOST_OUT" | grep -E 'will be' | head; fail "the create proposed is not $ALPHA_ADDR"; }

# The negative, stated as the classification rather than as the plan's
# arithmetic. "0 to destroy" is a sum; this is the section underneath it.
#
# The literal is "Owned and undeclared:", which is what
# internal/command/views/live_plan.go prints for foreign.Result.Removals -
# NOT the field name. A grep for "Removals:" would never match anything in
# any run and would pass here for the wrong reason.
grep -qF 'Owned and undeclared:' <<< "$LOST_OUT" \
  && { grep -A8 -F 'Owned and undeclared:' <<< "$LOST_OUT"
       fail "the run classified something as owned-and-undeclared with a located record missing. A lost note about where an object is is not permission to delete it."; }
grep -qE "will be destroyed" <<< "$LOST_OUT" \
  && { grep -E 'will be destroyed' <<< "$LOST_OUT"; fail "the run proposed destroying something with a located record missing"; }
grep -qE '^Foreign resources: none among the [0-9]+ types? swept' <<< "$LOST_OUT" \
  || { grep -E '^Foreign resources:' <<< "$LOST_OUT"; fail "the foreign sweep did not complete cleanly with a located record missing"; }

# And the bound on that negative, taken from the run's own words rather than
# left implicit. The located types are NOT enumerated by any sweep - they
# carry no marker, so there is nothing to sweep them by - and the run says
# so by name. That is what makes "nothing was classified for removal"
# honest here rather than merely true: it is true because these types are
# never candidates, and the operator is told that in the same output.
#
# It is also why issue #270's promise that a lost record's object "is
# reported as unclaimed" is not delivered today. Nothing lists these types,
# so nothing can report them as unclaimed. Destroyed is what must not
# happen, and does not; unclaimed is what the message offers, and no sweep
# can currently produce it.
grep -qF '[NOT_SCANNED]' <<< "$LOST_OUT" \
  || fail "the run did not report which types went unswept, so 'nothing was classified for removal' cannot be read as anything but silence"
grep -qF 'aws_cloudfront_public_key' <<< "$(grep -A1 -F '[NOT_SCANNED]' <<< "$LOST_OUT"; grep -B1 -F '[NOT_SCANNED]' <<< "$LOST_OUT")" \
  || { grep -B2 -A8 -F '[NOT_SCANNED]' <<< "$LOST_OUT"
       fail "aws_cloudfront_public_key is not named among the unswept types. Either it is now being swept - in which case the unclaimed report above should exist and this step should assert it - or the section moved."; }

# And the live object is still there. This is the assertion the whole trade
# rests on: the record is gone and the object is not.
STILL="$(awsl cloudfront get-public-key --id "$ALPHA_LIVE" --query 'PublicKey.PublicKeyConfig.Name' --output text 2>/dev/null || true)"
[ "$STILL" = "rl-e2e-alpha" ] \
  || fail "the live key $ALPHA_LIVE is gone or renamed after the plan that lost its record; it should be untouched"
log "  1 create proposed for $ALPHA_ADDR, 0 destroys, no removal, and $ALPHA_LIVE is still live"

# The announced duplicate, actually announced. Applying that plan is what an
# operator would do next, and the ruling is that it makes a SECOND object -
# visible in the plan, in the apply, and in the account - rather than
# quietly deleting the first.
APPLY3_OUT="$(cd "$MAIN" && "$TOFU" apply -input=false -auto-approve -no-color 2>&1)" || {
  strip_trace <<< "$APPLY3_OUT" | tail -30; fail "the recovery apply failed"; }
grep -qE 'Apply complete! Resources: 1 added, 0 changed, 0 destroyed' <<< "$APPLY3_OUT" \
  || { grep -E 'Apply complete' <<< "$APPLY3_OUT"; fail "the recovery apply did not add exactly one resource and destroy none"; }

DUPES="$(awsl cloudfront list-public-keys --query 'PublicKeyList.Items[].Id' --output text | tr '\t' '\n' | grep -c . || true)"
[ "$DUPES" = "3" ] || fail "expected 3 live public keys after the recovery apply (the duplicate is the announced cost), got $DUPES"
STILL="$(awsl cloudfront get-public-key --id "$ALPHA_LIVE" --query 'PublicKey.PublicKeyConfig.Name' --output text 2>/dev/null || true)"
[ "$STILL" = "rl-e2e-alpha" ] \
  || fail "the original key $ALPHA_LIVE was destroyed by the recovery apply. A lost record must cost a duplicate, never a deletion."
NEW_ALPHA="$(record_id "$ALPHA_REC")"
[ "$NEW_ALPHA" != "$ALPHA_LIVE" ] || fail "the recovery apply recorded the OLD id, so nothing was created"
log "  the duplicate is announced and the original survives: 3 live keys, alpha's record now $NEW_ALPHA"

# ── 11. no state file, ever ─────────────────────────────────────────────────
rm -f "$MAIN/terraform.tfstate" "$MAIN/terraform.tfstate.backup"
set +e
( cd "$MAIN" && "$TOFU" live-plan -input=false -no-color >/dev/null 2>&1 )
set -e
[ ! -f "$MAIN/terraform.tfstate" ] \
  || fail "a state file exists after live-plan - it must never be read or written"

log ""
log "=== PASS ==="
log ""
log "An object with nowhere to carry a marker, whose identity the provider"
log "minted, was found again after the state file was deleted - by the"
log "estate's record store, at the id the emulator agrees with. A lost record"
log "cost a duplicate and destroyed nothing."
log ""
log "If step 7 goes red the VALUE moved while every verdict stayed green."
log "Read internal/live/projection/located.go, not the plan output."
