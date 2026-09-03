# identity-is-a-tag
# CLAIM 7 - Identity is a tag you can read and move: estates isolate by tag, any AWS tool answers ownership, and a rename is a retag. ~3 min.

SMOKE_WORK="$SMOKE_WORKROOT/idtag"
mkdir -p "$SMOKE_WORK/a" "$SMOKE_WORK/b"
for c in a b; do
  cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/$c/"
  rm -rf "$SMOKE_WORK/$c/README.md"
  python3 - "$SMOKE_WORK/$c" "smoke-idtag-$c" <<'PYEOF'
import sys, pathlib
d, name = sys.argv[1], sys.argv[2]
for f in pathlib.Path(d).glob('*.tf'):
    f.write_text(f.read_text().replace('stateless-e2e-block', name))
PYEOF
done
export SMOKE_WORK

stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "the claim"
explain \
  "Identity is a tag you can read and move. Because ownership lives on" \
  "each resource as two tags, three things follow that stock cannot" \
  "offer: estates in one account are isolated by construction, any AWS" \
  "tool can answer ownership without this tool present, and renaming a" \
  "resource in code is a tag rewrite where stock demands state surgery."

step "1. two estates stand up in one account"
cmd "choudoufu apply -auto-approve   # in estate a, then estate b"
for c in a b; do
  ( cd "$SMOKE_WORK/$c" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "idtag" "estate $c init failed"
  ( cd "$SMOKE_WORK/$c" && chdf apply -auto-approve -input=false -no-color >/dev/null 2>&1 ) || fail "idtag" "estate $c apply failed"
done
proof "two estates, one account, one emulator. Nothing separates them but their tags."

step "2. any AWS tool answers ownership - this one is the plain CLI"
explain \
  "No choudoufu below, just the tagging API every AWS SDK exposes. The" \
  "estate tag scopes the estate; the address tag names each resource's" \
  "place in code. A cost tool, or a coworker with nothing but read" \
  "access, sees exactly what the plan sees."
cmd "aws resourcegroupstaggingapi get-resources --tag-filters Key=tofu-estate,Values=smoke-idtag-a"
A_ARNS="$(awsl resourcegroupstaggingapi get-resources --tag-filters Key=tofu-estate,Values=smoke-idtag-a --query 'ResourceTagMappingList[].ResourceARN' --output text | tr '\t' '\n' | grep -c . || true)"
B_ARNS="$(awsl resourcegroupstaggingapi get-resources --tag-filters Key=tofu-estate,Values=smoke-idtag-b --query 'ResourceTagMappingList[].ResourceARN' --output text | tr '\t' '\n' | grep -c . || true)"
[ "$A_ARNS" -gt 0 ] 2>/dev/null || fail "idtag" "the tagging API lists nothing for estate a"
[ "$B_ARNS" -gt 0 ] 2>/dev/null || fail "idtag" "the tagging API lists nothing for estate b"
SAMPLE="$(awsl resourcegroupstaggingapi get-resources --tag-filters Key=tofu-estate,Values=smoke-idtag-a --query 'ResourceTagMappingList[0].Tags[?Key==`tofu-address`].Value' --output text)"
echo "estate a: $A_ARNS resources; estate b: $B_ARNS; a sample address tag reads '$SAMPLE'" | evidence
proof "ownership answered by the platform's own API - the tool did not have to be in the room."

step "3. neither estate can see the other"
cmd "choudoufu plan   # in each estate, while both are live"
for c in a b; do
  P="$(cd "$SMOKE_WORK/$c" && chdf plan -input=false -no-color 2>&1)" || fail "idtag" "estate $c plan failed"
  grep -q "No changes." <<< "$P" || fail "idtag" "estate $c does not plan clean beside its neighbour: $P"
  grep -qE "smoke-idtag-[^$c]" <<< "$P" && fail "idtag" "estate $c's plan mentions the other estate"
done
proof "both plans clean, neither naming the other's resources. The tag is the boundary."

step "4. a rename is a retag, not surgery"
explain \
  "The everyday rename is aws_vpc.main becoming aws_vpc.core in code." \
  "Stock needs 'state mv' - hand-editing the record to match the code." \
  "Here the record is the tag, and live-mv rewrites it on the live" \
  "resource. The resource itself is never touched beyond that tag."
cmd "edit the block name ; choudoufu live-mv aws_vpc.main aws_vpc.core ; choudoufu plan"
python3 - "$SMOKE_WORK/a" <<'PYEOF'
import sys, pathlib
d = sys.argv[1]
for f in pathlib.Path(d).glob('*.tf'):
    t = f.read_text()
    t = t.replace('resource "aws_vpc" "main"', 'resource "aws_vpc" "core"').replace('aws_vpc.main', 'aws_vpc.core')
    f.write_text(t)
PYEOF
if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control: live-mv is deliberately SKIPPED after the code" \
    "rename. The live vpc still wears the old address, so the plan must" \
    "treat the new name as missing and the old one as orphaned - the" \
    "destroy-and-recreate stock would inflict. A clean plan here would" \
    "mean the retag never mattered."
  BP="$(cd "$SMOKE_WORK/a" && chdf plan -input=false -no-color 2>&1)" || fail "idtag" "BREAK plan failed: $BP"
  if grep -q "No changes." <<< "$BP"; then
    fail "idtag" "BREAK: the plan is clean without the retag - live-mv proves nothing"
  fi
  grep -E "to add|to destroy" <<< "$BP" | head -1 | evidence
  proof "caught - without the retag the rename is a destroy-and-recreate, exactly stock's behaviour. Identity lives in the tag."
  for c in a b; do
    ( cd "$SMOKE_WORK/$c" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  done
  exit 0
fi
MVOUT="$(cd "$SMOKE_WORK/a" && chdf live-mv aws_vpc.main aws_vpc.core -no-color 2>&1)" \
  || fail "idtag" "live-mv failed: $MVOUT"
NEWADDR="$(awsl ec2 describe-vpcs --filters "Name=tag:tofu-estate,Values=smoke-idtag-a" --query 'Vpcs[0].Tags[?Key==`tofu-address`].Value' --output text)"
[ "$NEWADDR" = "aws_vpc.core" ] || fail "idtag" "the address tag reads '$NEWADDR', not aws_vpc.core - the retag did not land"
echo "the vpc's tofu-address tag now reads aws_vpc.core" | evidence
P4="$(cd "$SMOKE_WORK/a" && chdf plan -input=false -no-color 2>&1)" || fail "idtag" "post-rename plan failed: $P4"
grep -q "No changes." <<< "$P4" || fail "idtag" "the rename was not free: $P4"
proof "one tag write and the plan is clean. No state surgery, no destroy, no recreate."

step "5. teardown - both estates"
cmd "choudoufu apply -destroy -auto-approve   # in each"
for c in a b; do
  ( cd "$SMOKE_WORK/$c" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
    || fail "idtag" "estate $c teardown failed"
done
proof "both estates gone, each by its own destroy, neighbours to the end."

echo "  What you watched: two estates share an account with nothing but tags"
echo "  between them, the plain AWS CLI answer who owns what, and a code"
echo "  rename settle as one tag write where stock would demand surgery on"
echo "  its state file."
