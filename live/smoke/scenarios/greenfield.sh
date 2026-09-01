# greenfield
# A new estate from nothing: markers ride the creates, the replan is empty, the cache is disposable, destroy is exact. ~1 min.
# Proves markers ride the create calls, the replan is empty, the #685
# cache exists and is disposable, and destroy tears down exactly what was
# made. BREAK=1 rewrites one live marker and requires the replan to catch
# it.

SMOKE_WORK="$SMOKE_WORKROOT/greenfield"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-block/." "$SMOKE_WORK/"
rm -rf "$SMOKE_WORK/README.md"

stack_up

export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1

step "1. init + plain apply from an empty account"
( cd "$SMOKE_WORK" && chdf init -input=false -no-color >/dev/null ) || fail "greenfield" "init failed"
APPLY_OUT="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "greenfield" "apply failed: $APPLY_OUT"
grep -qE 'Apply complete! Resources: [1-9][0-9]* added' <<< "$APPLY_OUT" \
  || fail "greenfield" "apply reported no additions: $APPLY_OUT"
ADDED="$(grep -oE 'Resources: [0-9]+ added' <<< "$APPLY_OUT" | grep -oE '[0-9]+')"
note "apply: $ADDED added, no authoritative state file"

step "2. the markers rode the create calls"
LG_TAGS="$(awsl logs list-tags-log-group --log-group-name "/stateless-e2e-block/app" --query 'tags' --output json 2>/dev/null || echo '{}')"
grep -q '"tofu-estate"' <<< "$LG_TAGS" && grep -q '"tofu-address"' <<< "$LG_TAGS" \
  || fail "greenfield" "the log group carries no ownership markers: $LG_TAGS"
note "log group carries tofu-estate + tofu-address, readable by any cloud tool"

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - rewrite a live marker; the replan must catch it"
  awsl logs tag-log-group --log-group-name "/stateless-e2e-block/app" \
    --tags tofu-address="aws_cloudwatch_log_group.hijacked" \
    || fail "greenfield" "BREAK: could not rewrite the marker"
  BOUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 || true)"
  if grep -q "No changes." <<< "$BOUT"; then
    fail "greenfield" "BREAK: the plan is still empty after a live marker was rewritten - the empty-replan assertion below is scenery"
  fi
  note "caught: the rewritten marker changed the plan, so the assertions are load-bearing"
  exit 0
fi

step "3. the replan is empty"
PLAN_OUT="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "greenfield" "plan failed: $PLAN_OUT"
grep -q "No changes." <<< "$PLAN_OUT" || fail "greenfield" "replan is not empty: $PLAN_OUT"
note "No changes. - prior state was a projection of the live system"

step "4. the #685 cache: present, disposable, changes nothing"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
[ -f "$CACHE" ] || fail "greenfield" "no cache at $CACHE after a plain apply"
rm -f "$CACHE"
PLAN2="$(cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1)" \
  || fail "greenfield" "plan without the cache failed"
grep -q "No changes." <<< "$PLAN2" || fail "greenfield" "deleting the cache changed the plan: $PLAN2"
note "cache deleted; the plan did not change - staleness costs reads, never results"

step "5. destroy tears down exactly what was made"
DESTROY_OUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "greenfield" "apply -destroy failed: $DESTROY_OUT"
grep -qE "Resources: 0 added, 0 changed, $ADDED destroyed" <<< "$DESTROY_OUT" \
  || fail "greenfield" "destroy did not remove exactly the $ADDED created resources: $DESTROY_OUT"
note "$ADDED destroyed - the estate is gone, and nothing else was touched"
