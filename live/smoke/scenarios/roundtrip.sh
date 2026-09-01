# roundtrip
# CLAIM 6 - The roundtrip: one command in, one file out. A stock estate is adopted with live-import and handed back as a stock state file. ~3 min.

SMOKE_WORK="$SMOKE_WORKROOT/roundtrip"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"
python3 - "$SMOKE_WORK" <<'PYEOF'
import re, sys
d = sys.argv[1]
src = open(f'{d}/versions.tf').read().replace('stateless-e2e-block', 'smoke-roundtrip')
open(f'{d}/versions-live.tf.keep', 'w').write(src)
stock = re.sub(r'\n  live \{\n    estate = "smoke-roundtrip"\n  \}\n', '\n', src)
assert 'live {' not in stock
open(f'{d}/versions-stock.tf.keep', 'w').write(stock)
PYEOF
use_versions() { cp "$SMOKE_WORK/versions-$1.tf.keep" "$SMOKE_WORK/versions.tf"; }

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "The roundtrip means one command in, one file out. Migrating to a new" \
  "state tool is usually a trapdoor - once your estate is in, the only" \
  "way back is another migration project. Here the door in is one" \
  "command (live-import reads the state file you already have and" \
  "stamps ownership markers on what verifies) and the door out is one" \
  "file (the local cache IS a stock-format state file, ready to hand" \
  "back). This scenario walks the whole loop and lets stock destroy" \
  "the estate at the end, using the file choudoufu handed back."

step "1. stock stands the estate up, tagless"
explain \
  "For the before picture, the PINNED stock OpenTofu, in its own" \
  "container, applies the estate the ordinary way. Nothing here carries a marker -" \
  "the config declares no tags at all, so this is the hardest starting" \
  "point: identity exists only in terraform.tfstate."
cmd "docker compose run opentofu apply -auto-approve"
use_versions stock
stock init -input=false -no-color >/dev/null 2>&1 || fail "roundtrip" "stock init failed"
SOUT="$(stock apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "roundtrip" "stock apply failed: $SOUT"
ADDED="$(grep -oE 'Resources: [0-9]+ added' <<< "$SOUT" | grep -oE '[0-9]+')"
[ -n "$ADDED" ] || fail "roundtrip" "stock apply added nothing: $SOUT"
[ -f "$SMOKE_WORK/terraform.tfstate" ] || fail "roundtrip" "no terraform.tfstate after the stock apply"
MARKED="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=smoke-roundtrip" --query 'length(Vpcs)' --output text)"
[ "$MARKED" = "0" ] || fail "roundtrip" "resources are already marked before live-import ran"
proof "$ADDED resources, one state file, zero markers. A plain stock estate."

step "2. the door in - one command"
if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control: live-import is deliberately SKIPPED. Turning on the" \
    "live block does not bind resources you already manage - that is the" \
    "documented quiet failure of every migration. The plan below must" \
    "propose building a duplicate estate; if it comes back clean without" \
    "the markers, the binding this claim rests on never mattered."
else
  explain \
    "The entire migration: live-import reads terraform.tfstate once," \
    "read-only, verifies every resource it names against the live" \
    "system, and stamps the two ownership markers on what verifies." \
    "No resource is touched beyond its tags; the state file is not" \
    "modified at all."
  cmd "choudoufu live-import -state=terraform.tfstate -estate=smoke-roundtrip -approve"
  use_versions live
  ( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "roundtrip" "choudoufu init before live-import failed"
  IOUT="$(cd "$SMOKE_WORK" && chdf live-import -state=terraform.tfstate -estate=smoke-roundtrip -approve -no-color 2>&1)" \
    || fail "roundtrip" "live-import failed: $IOUT"
  grep -iE "ratif|marked|stamp" <<< "$IOUT" | head -2 | evidence
  MARKED="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=smoke-roundtrip" --query 'length(Vpcs)' --output text)"
  [ "$MARKED" = "1" ] || fail "roundtrip" "live-import did not mark the vpc"
  proof "one command, and identity now lives on the resources instead of in the file."
fi

step "3. bound - and the old record is now optional"
use_versions live
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "roundtrip" "choudoufu init failed"
P1="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" || fail "roundtrip" "choudoufu plan failed: $P1"
if [ "${BREAK:-0}" = "1" ]; then
  if grep -q "No changes." <<< "$P1"; then
    fail "roundtrip" "BREAK: the plan is clean with no markers stamped - binding never depended on live-import"
  fi
  grep -E "to add" <<< "$P1" | head -1 | evidence
  proof "caught - without the one command, the plan proposes a duplicate estate beside the real one. Adoption is the markers, and live-import is what writes them."
  use_versions stock
  stock apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 || true
  exit 0
fi
grep -q "No changes." <<< "$P1" || fail "roundtrip" "the adopted estate did not plan clean: $P1"
explain \
  "The state file has done its last job. It can go - and an ordinary" \
  "apply now keeps the estate converged and refreshes the local cache."
cmd "rm terraform.tfstate ; choudoufu apply -auto-approve"
rm "$SMOKE_WORK/terraform.tfstate"
AOUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "roundtrip" "the post-adoption apply failed: $AOUT"
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$AOUT" \
  || fail "roundtrip" "the adopted estate was not a no-op to apply: $AOUT"
[ -f "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate" ] \
  || fail "roundtrip" "no cache after the apply - nothing to hand back on the way out"
proof "state file deleted, estate unmoved. The cache beside it is a stock-format state file, and that is the exit."

step "4. the door out - one file"
explain \
  "Leaving is copying the cache to terraform.tfstate and removing the" \
  "live block. Stock's first plan back proposes exactly one kind of" \
  "change - removing the two marker tags, the only thing choudoufu ever" \
  "added. One apply later, nothing of the fork remains anywhere."
cmd "cp .terraform/choudoufu-cache.tfstate terraform.tfstate ; remove the live block ; stock plan"
cp "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate" "$SMOKE_WORK/terraform.tfstate"
use_versions stock
POUT="$(stock plan -input=false -no-color 2>&1)" || fail "roundtrip" "stock refused the handed-back state: $POUT"
grep -qE 'Plan: 0 to add, [0-9]+ to change, 0 to destroy|No changes.' <<< "$POUT" \
  || fail "roundtrip" "the exit plan proposes more than tag removals: $POUT"
grep -E 'Plan: |No changes.' <<< "$POUT" | head -1 | evidence
if ! grep -q "No changes." <<< "$POUT"; then
  grep -qE 'tofu-estate|tofu-address' <<< "$POUT" \
    || fail "roundtrip" "the exit plan changes something other than the markers: $POUT"
  stock apply -auto-approve -input=false -no-color >/dev/null 2>&1 \
    || fail "roundtrip" "the marker-removal apply failed"
  P2="$(stock plan -input=false -no-color 2>&1)" || fail "roundtrip" "stock replan failed"
  grep -q "No changes." <<< "$P2" || fail "roundtrip" "stock is not converged after removing the markers: $P2"
fi
proof "stock accepted the file and stripped the markers; it owns the estate again. The roundtrip is closed."

step "5. teardown - by stock, from the handed-back file"
cmd "docker compose run opentofu apply -destroy -auto-approve"
DOUT="$(stock apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "roundtrip" "stock destroy failed: $DOUT"
grep -qE "Resources: 0 added, 0 changed, $ADDED destroyed" <<< "$DOUT" \
  || fail "roundtrip" "stock did not destroy all $ADDED resources: $DOUT"
proof "$ADDED destroyed by stock alone. The estate came back whole; the exit is one file."

echo "  What you watched: a tagless stock estate adopted with one command,"
echo "  operated with its state file deleted, then handed back as a plain"
echo "  state file that stock could plan, converge, and destroy with. The"
echo "  only trace of the fork was two tags, and the exit removed them."
