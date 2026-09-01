# greenfield
# A new estate from nothing: markers ride the creates, the replan is empty, the cache is disposable, destroy is exact. ~1 min.

SMOKE_WORK="$SMOKE_WORKROOT/greenfield"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"

stack_up

export AWS_ENDPOINT_URL="http://localhost:${FLOCI_PORT}"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "1. a new estate, one plain apply"
explain \
  "The configuration has a live block and nothing else - no backend, no" \
  "lock table, nothing pre-created. Stock OpenTofu would write an" \
  "authoritative terraform.tfstate here; choudoufu instead writes two" \
  "ownership tags onto each resource AS IT IS CREATED, and keeps only a" \
  "disposable cache. Watch the apply: it reads like stock."
cmd "choudoufu init && choudoufu apply -auto-approve"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "greenfield" "init failed"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "greenfield" "apply failed: $APPLY_OUT"
grep -E 'Apply complete!' <<< "$APPLY_OUT" | evidence
grep -qE 'Apply complete! Resources: [1-9][0-9]* added' <<< "$APPLY_OUT" \
  || fail "greenfield" "apply reported no additions: $APPLY_OUT"
ADDED="$(grep -oE 'Resources: [0-9]+ added' <<< "$APPLY_OUT" | grep -oE '[0-9]+')"
[ ! -f "$SMOKE_WORK/terraform.tfstate" ] \
  || fail "greenfield" "a terraform.tfstate appeared - a live-block apply must never write an authoritative state file"
proof "$ADDED resources created, and no terraform.tfstate exists. The record of ownership is on the resources themselves - the next step reads it back."

step "2. the markers, read back with the AWS CLI - no choudoufu in the loop"
explain \
  "If the ownership record really is on the resources, any cloud tool can" \
  "read it. This asks AWS (the emulator) directly for the log group's" \
  "tags. tofu-estate says which estate owns it; tofu-address says which" \
  "resource block it is. Your IAM can condition on these tags - that is" \
  "the whole permission model."
cmd "aws logs list-tags-log-group --log-group-name /stateless-e2e-block/app"
LG_TAGS="$(awsl logs list-tags-log-group --log-group-name "/stateless-e2e-block/app" --query 'tags' --output json 2>/dev/null || echo '{}')"
grep -E 'tofu-(estate|address)' <<< "$LG_TAGS" | evidence
grep -q '"tofu-estate"' <<< "$LG_TAGS" && grep -q '"tofu-address"' <<< "$LG_TAGS" \
  || fail "greenfield" "the log group carries no ownership markers: $LG_TAGS"
proof "the markers rode the create call itself. There was no second write to lose, no window where the resource existed unowned."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - rewrite a live marker; the replan must catch it"
  explain \
    "You asked for proof the assertions can fail. This rewrites the log" \
    "group's tofu-address to point at a resource block that does not" \
    "exist - the kind of corruption a bad migration or a hostile tag edit" \
    "would cause. If the next plan is still empty, this whole scenario is" \
    "scenery and the run fails itself."
  cmd "aws logs tag-log-group ... tofu-address=aws_cloudwatch_log_group.hijacked"
  awsl logs tag-log-group --log-group-name "/stateless-e2e-block/app" \
    --tags tofu-address="aws_cloudwatch_log_group.hijacked" \
    || fail "greenfield" "BREAK: could not rewrite the marker"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "greenfield" "BREAK: the plan is still empty after a live marker was rewritten - the empty-replan assertion below is scenery"
  fi
  grep -E '^Plan:|Error:' <<< "$BOUT" | head -2 | evidence
  proof "caught. The rewritten marker changed the plan, so every empty-plan claim in this scenario is a real check."
  exit 0
fi

step "3. the replan - prior state rebuilt from the live system"
explain \
  "Now the question every state-file user asks: with no state file, how" \
  "does the next plan know what exists? It reads the live system - the" \
  "markers say which object is which - and builds prior state fresh. If" \
  "identity binding is right, the plan is empty. If it were wrong, you" \
  "would see creates or destroys here."
cmd "choudoufu plan"
PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "greenfield" "plan failed: $PLAN_OUT"
grep -E 'No changes\.' <<< "$PLAN_OUT" | head -1 | evidence
grep -q "No changes." <<< "$PLAN_OUT" || fail "greenfield" "replan is not empty: $PLAN_OUT"
proof "an empty plan, with nothing stored anywhere that could have remembered the estate. The live system was the memory."

step "4. the state cache - present, disposable, and never trusted"
explain \
  "The apply also wrote a state cache under .terraform/ - a speed-up," \
  "never a record. The ruling it lives under: never consulted for" \
  "ownership, live wins any disagreement, losing it costs a slower run" \
  "and nothing else. So: delete it, replan, and require the identical" \
  "answer."
cmd "rm .terraform/choudoufu-cache.tfstate && choudoufu plan"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
[ -f "$CACHE" ] || fail "greenfield" "no cache at $CACHE after a plain apply"
rm -f "$CACHE"
PLAN2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "greenfield" "plan without the cache failed"
grep -E 'No changes\.' <<< "$PLAN2" | head -1 | evidence
grep -q "No changes." <<< "$PLAN2" || fail "greenfield" "deleting the cache changed the plan: $PLAN2"
proof "the cache was there and its loss changed nothing. This is the file stock OpenTofu cannot afford to lose, made losable."

step "5. destroy - exactly what was made, nothing else"
explain \
  "Teardown is the last claim: destroy must remove exactly the $ADDED" \
  "resources this scenario created - identified by their markers - and" \
  "must not touch anything else living in the account."
cmd "choudoufu apply -destroy -auto-approve"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "greenfield" "apply -destroy failed: $DESTROY_OUT"
grep -E 'Destroy complete|Apply complete' <<< "$DESTROY_OUT" | head -1 | evidence
grep -qE "Resources: 0 added, 0 changed, $ADDED destroyed" <<< "$DESTROY_OUT" \
  || fail "greenfield" "destroy did not remove exactly the $ADDED created resources: $DESTROY_OUT"
proof "$ADDED destroyed, 0 added, 0 changed. The estate is gone."

echo "  What you watched: an estate live its whole life - created with its"
echo "  ownership on itself, replanned from the live system, surviving the"
echo "  loss of its cache, and torn down exactly - without an authoritative"
echo "  state file ever existing."
