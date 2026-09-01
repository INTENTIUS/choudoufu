# backend-sets-itself-up
# CLAIM 4 - The live backend sets itself up automatically when configured: declaring it is the whole setup. ~1 min.

SMOKE_WORK="$SMOKE_WORKROOT/autosetup"
mkdir -p "$SMOKE_WORK/a" "$SMOKE_WORK/b"; export SMOKE_WORK
cat > "$SMOKE_WORK/a/main.tf" <<'TFEOF'
terraform {
  live {
    estate = "smoke-auto-local"
  }
}

resource "terraform_data" "effect" {
  input = "v1"
}
TFEOF
sed 's/smoke-auto-local/smoke-auto-ssm/; s|estate = "smoke-auto-ssm"|estate = "smoke-auto-ssm"\n\n    record_store "ssm" {}|' \
  "$SMOKE_WORK/a/main.tf" > "$SMOKE_WORK/b/main.tf"

step "the claim"
explain \
  "The live backend sets itself up when configured. Compare stock's" \
  "remote-backend day one: create a bucket, enable versioning, create a" \
  "lock table, write IAM for both, keep them in step forever, then run" \
  "init and answer migration prompts. Here the backend is three pieces" \
  "that live where AWS already is - identity as tags, values in a record" \
  "store, effects as receipts - and DECLARING them is the entire setup." \
  "Each store also proves itself before any plan trusts it: at first use" \
  "it writes a sentinel and reads it back through the same List call" \
  "plans use, so a store that cannot answer refuses loudly instead of" \
  "impersonating an empty estate."

step "1. no store declared - the local one appears unbidden"
explain \
  "Copy A declares a live block, one record-backed resource, and NOTHING" \
  "about storage. The way stock implies a local state file, a live block" \
  "implies a local record store: a .tofu-records directory beside the" \
  "module, created at first use, sentinel first."
cmd "choudoufu apply -auto-approve   # no record_store anywhere in the config"
( cd "$SMOKE_WORK/a" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "auto" "init failed"
A_OUT="$(cd "$SMOKE_WORK/a" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "auto" "apply failed: $A_OUT"
grep -qE 'Resources: 1 added' <<< "$A_OUT" || fail "auto" "the record-backed resource did not apply: $A_OUT"
[ -d "$SMOKE_WORK/a/.tofu-records" ] \
  || fail "auto" "no .tofu-records directory appeared - the implied local store did not set itself up"
find "$SMOKE_WORK/a/.tofu-records" -name '.store-sentinel' | head -1 | sed "s|$SMOKE_WORK/a/||" | evidence
[ -n "$(find "$SMOKE_WORK/a/.tofu-records" -name '.store-sentinel')" ] \
  || fail "auto" "the store exists but never provisioned its sentinel"
proof "a directory nobody configured, holding a sentinel the store wrote to prove its own read path, plus the resource's record. Zero setup steps."

step "2. it works: the effect survives between runs"
cmd "choudoufu plan"
P_A="$(cd "$SMOKE_WORK/a" && chdf plan -input=false -no-color 2>&1)" || fail "auto" "replan failed: $P_A"
grep -E 'No changes\.' <<< "$P_A" | head -1 | evidence
grep -q "No changes." <<< "$P_A" || fail "auto" "the record-backed resource did not survive the replan: $P_A"
proof "the store is not scaffolding - the resource's identity round-tripped through it."

step "3. a cloud store is one declaration, and it provisions itself"
explain \
  "Copy B declares record_store \"ssm\" {} and nothing else - no" \
  "parameter created ahead of time, no path chosen, no IAM beyond what" \
  "the run already has. First use writes the sentinel INTO Parameter" \
  "Store and reads it back through List; the AWS CLI can then show the" \
  "parameter the store provisioned for itself."
stack_up
export AWS_ENDPOINT_URL="$SMOKE_ENDPOINT"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
cmd "choudoufu apply -auto-approve   # record_store \"ssm\" {} is the whole setup"
( cd "$SMOKE_WORK/b" && chdf init -input=false -no-color >/dev/null 2>&1 ) || fail "auto" "copy b init failed"
B_OUT="$(cd "$SMOKE_WORK/b" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "auto" "the ssm-backed apply failed: $B_OUT"
SENTINEL_PARAM="$(awsl ssm describe-parameters --query 'Parameters[].Name' --output text | tr '\t' '\n' | grep 'store-sentinel' | head -1)"
[ -n "$SENTINEL_PARAM" ] || fail "auto" "no sentinel parameter exists in SSM - the declared store never provisioned itself"
awsl ssm get-parameter --name "$SENTINEL_PARAM" --query 'Parameter.Value' --output text | head -c 90 | evidence
echo | evidence
proof "the store provisioned itself on first use, and the sentinel's own payload says what it is for - readable by any cloud tool, like everything else here."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - a store that cannot answer must refuse, never impersonate emptiness"
  explain \
    "The corruption the sentinel exists for: the SSM store becomes" \
    "unreachable (only SSM - the provider's endpoint stays healthy, via" \
    "the SDK's service-specific override). Before the sentinel, a store" \
    "whose List returned nothing read as an empty estate and the plan" \
    "proposed re-creating live resources. Now the run must REFUSE, and" \
    "the refusal must name the store - if it plans anything at all, the" \
    "self-verification this claim rests on is scenery."
  cmd "AWS_ENDPOINT_URL_SSM=http://localhost:9 choudoufu plan"
  BOUT="$(cd "$SMOKE_WORK/b" && AWS_ENDPOINT_URL_SSM=http://localhost:9 AWS_MAX_ATTEMPTS=1 chdf plan -input=false -no-color 2>&1)" && \
    fail "auto" "the plan SUCCEEDED against an unreachable record store: $BOUT"
  grep -qiE "record.store|sentinel" <<< "$BOUT" \
    || fail "auto" "the run failed but nothing named the record store - an anonymous failure is not the loud refusal the claim promises: $BOUT"
  grep -iE "record.store|sentinel" <<< "$BOUT" | head -1 | evidence
  proof "caught: unreachable means refused-by-name, never an empty-looking estate. The sentinel is why."
  exit 0
fi

step "4. teardown - both stores, no residue beyond their own directories"
cmd "choudoufu apply -destroy -auto-approve   # in both copies"
( cd "$SMOKE_WORK/a" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "auto" "copy a teardown failed"
( cd "$SMOKE_WORK/b" && chdf apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) \
  || fail "auto" "copy b teardown failed"
proof "both estates gone. Nothing to deprovision, because nothing was ever provisioned by hand."

echo "  What you watched: a store nobody configured appear with its sentinel"
echo "  already written, a cloud store provision itself from one declaration,"
echo "  and the setup ceremony stock requires - buckets, lock tables, IAM"
echo "  pairs, migration prompts - simply not exist."
