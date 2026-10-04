# k8s-roundtrip
# CLAIM 6 (kubernetes) - One command in, one file out. ~3 min.
#
# This proof: an estate stock Terraform stood up on a real cluster is adopted
# with one live-import, runs with its state file deleted, and is handed back
# as the cache, which stock Terraform plans, converges and destroys.
#
# DRAFT: written and never run (live/smoke/drafts/README.md). Claim 6's
# Kubernetes cell stays open until both arms have run green.
#
# The oracle here is stock Terraform on PATH, the binary k8s-smoke.yml
# installs at live/oracle-versions.json's terraform_version, as every other
# Kubernetes scenario uses it. The AWS proof's oracle is stock OpenTofu in a
# container on the emulator's network, which cannot reach a kind API server
# on the host's loopback. That costs one step on the way out: the cache
# names its provider as registry.opentofu.org/hashicorp/kubernetes, and
# Terraform resolves the same source against registry.terraform.io, so the
# handed-back file goes through `terraform state replace-provider` once.
# That is Terraform's own command for exactly this, and an OpenTofu user
# leaving does not need it; the step says so rather than hiding it.
#
# BREAK=1 skips live-import and requires the live plan to refuse the
# unlabelled objects by name rather than plan clean: adoption is the label
# live-import writes, so the clean plan in step 3 is shown to depend on it.

command -v terraform >/dev/null 2>&1 \
  || fail "k8s-roundtrip" "the terraform binary is not on PATH - this scenario needs the stock oracle on both doors"
W="$SMOKE_WORKROOT/k8s-roundtrip"
STOCK="$W/stock"; LIVE="$W/live"
mkdir -p "$STOCK" "$LIVE"
SMOKE_WORK="$W"; export SMOKE_WORK
ESTATE="smoke-k8s-roundtrip"
cp "$ROOT/live/e2e/estate-k8s/main.tf" "$STOCK/main.tf"
cp "$ROOT/live/e2e/estate-k8s/main.tf" "$LIVE/main.tf"
python3 - "$ROOT/live/e2e/estate-k8s/versions.tf" "$STOCK/versions.tf" "$LIVE/versions.tf" "$ESTATE" <<'PYEOF'
import re, sys
src, stock_out, live_out, estate = sys.argv[1:5]
text = open(src).read()
live = text.replace('estate = "smoke-k8s"', 'estate = "%s"' % estate)
assert 'estate = "%s"' % estate in live
stock = re.sub(r'\n  live \{\n    estate = "[^"]+"\n  \}\n', '\n', live)
assert 'live {' not in stock
open(stock_out, 'w').write(stock)
open(live_out, 'w').write(live)
PYEOF
NS="smoke-k8s"

cluster_up

step "the claim"
explain \
  "The way in is one command: live-import reads the state file you" \
  "already have, finds each object it names on the cluster, and writes" \
  "the tofu-estate label on what it finds. The way out is one file: the" \
  "local cache is a stock-format state file. This walks the loop on a" \
  "real cluster and lets stock destroy the estate at the end from the" \
  "file choudoufu handed back."

step "1. stock stands the estate up, with no label anywhere"
explain \
  "Plain stock Terraform, no live block: a namespace, a ConfigMap, a" \
  "ServiceAccount and a Service, recorded in a terraform.tfstate. Identity" \
  "lives only in that file."
cmd "terraform init && terraform apply -auto-approve"
logged k8s-roundtrip-stock-init "k8s-roundtrip" "stock init failed" -- in_dir "$STOCK" terraform init -input=false -no-color
SOUT="$(cd "$STOCK" && terraform apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-roundtrip" "stock apply failed: $(tail -5 <<< "$SOUT")"
grep -qF "Apply complete! Resources: 4 added" <<< "$SOUT" \
  || fail "k8s-roundtrip" "stock did not create exactly the 4 objects: $(grep -E 'Apply complete' <<< "$SOUT")"
[ -f "$STOCK/terraform.tfstate" ] || fail "k8s-roundtrip" "stock left no terraform.tfstate"
PRE="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}' 2>/dev/null || true)"
[ -z "$PRE" ] || fail "k8s-roundtrip" "the stock-made ConfigMap already carries tofu-estate=$PRE"
grep -E 'Apply complete!' <<< "$SOUT" | evidence
proof "4 objects, one state file, no label on any of them."

logged k8s-roundtrip-live-init "k8s-roundtrip" "choudoufu init failed" -- in_dir "$LIVE" chdf init -input=false -no-color

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - skip live-import; the live plan must refuse the unlabelled objects"
  explain \
    "Turning on the live block does not bind objects stock made: nothing" \
    "on them says whose they are. If the plan came back clean here, the" \
    "binding step 3 shows would not depend on live-import at all."
  cmd "choudoufu plan   # no live-import"
  BRC=0
  BOUT="$(cd "$LIVE" && chdf plan -input=false -no-color 2>&1)" || BRC=$?
  grep -E '^Error: |^Plan:|No changes' <<< "$BOUT" | head -2 | evidence
  if grep -q "No changes." <<< "$BOUT"; then
    fail "k8s-roundtrip" "BREAK: the plan is clean with no label written, so binding never depended on live-import"
  fi
  [ "$BRC" = "1" ] || fail "k8s-roundtrip" "BREAK: the plan exited $BRC, want 1: $BOUT"
  grep -q 'Error: Unlabelled live object holds the declared name' <<< "$BOUT" \
    || fail "k8s-roundtrip" "BREAK: the plan does not refuse the unlabelled objects by name: $BOUT"
  proof "caught: without the one command the live plan refuses the stock-made objects by name. Adoption is the label, and live-import is what writes it."
  ( cd "$STOCK" && terraform apply -destroy -auto-approve -input=false -no-color >/dev/null 2>&1 ) || true
  exit 0
fi

step "2. the way in - one command"
explain \
  "live-import reads terraform.tfstate once, read-only, finds each object" \
  "by kind, namespace and name, and writes the tofu-estate label and the" \
  "address annotation as one merge patch. Nothing else on an object" \
  "moves, and the state file is not modified."
cmd "choudoufu live-import -state=../stock/terraform.tfstate -estate=$ESTATE   # read-only first"
DRY="$(cd "$LIVE" && chdf live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -no-color 2>&1)" \
  || fail "k8s-roundtrip" "live-import (read-only) failed: $DRY"
grep -E 'eligible for stamping' <<< "$DRY" | head -1 | evidence
grep -q '4 of 4 resource instance(s) are eligible for stamping' <<< "$DRY" \
  || fail "k8s-roundtrip" "not every object in the stock state is eligible: $DRY"
STATE_SUM="$(shasum "$STOCK/terraform.tfstate" | cut -d' ' -f1)"
cmd "choudoufu live-import -state=../stock/terraform.tfstate -estate=$ESTATE -approve"
IOUT="$(cd "$LIVE" && chdf live-import -state="$STOCK/terraform.tfstate" -estate="$ESTATE" -approve -no-color 2>&1)" \
  || fail "k8s-roundtrip" "live-import -approve failed: $IOUT"
SUMMARY="$(grep 'newly stamped' <<< "$IOUT" | tail -1 || true)"
echo "${SUMMARY:-no summary line}" | evidence
grep -q '4 resource(s) newly stamped' <<< "$SUMMARY" \
  || fail "k8s-roundtrip" "live-import did not stamp all 4 objects: ${SUMMARY:-$IOUT}"
grep -q '0 failed, 0 skipped' <<< "$SUMMARY" \
  || fail "k8s-roundtrip" "live-import skipped or failed something: $SUMMARY"
[ "$(shasum "$STOCK/terraform.tfstate" | cut -d' ' -f1)" = "$STATE_SUM" ] \
  || fail "k8s-roundtrip" "live-import modified the state file it read"
LBL="$(kc get configmap app-config -n "$NS" -o jsonpath='{.metadata.labels.tofu-estate}')"
[ "$LBL" = "$ESTATE" ] || fail "k8s-roundtrip" "the ConfigMap carries tofu-estate='$LBL' after live-import, want $ESTATE"
proof "one command: 4 objects carry the label, and the state file is byte-for-byte what stock wrote."

step "3. bound - and the state file is now optional"
cmd "choudoufu plan ; rm ../stock/terraform.tfstate ; choudoufu apply -auto-approve"
P1="$(cd "$LIVE" && chdf plan -input=false -no-color 2>&1)" || fail "k8s-roundtrip" "the plan after adoption failed: $P1"
grep -q "No changes." <<< "$P1" || fail "k8s-roundtrip" "the adopted estate does not plan clean: $P1"
rm -f "$STOCK/terraform.tfstate" "$STOCK/terraform.tfstate.backup"
AOUT="$(cd "$LIVE" && chdf apply -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-roundtrip" "the apply after adoption failed: $AOUT"
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$AOUT" \
  || fail "k8s-roundtrip" "the adopted estate was not a no-op to apply: $AOUT"
CACHE="$LIVE/.terraform/choudoufu-cache.tfstate"
[ -f "$CACHE" ] || fail "k8s-roundtrip" "no cache after the apply, so there is no file to hand back"
grep -E 'No changes\.' <<< "$P1" | head -1 | evidence
proof "state file deleted, estate unmoved, and a stock-format cache beside the configuration."

step "4. the way out - one file"
explain \
  "Leaving is copying the cache to terraform.tfstate and dropping the" \
  "live block. Terraform resolves hashicorp/kubernetes against its own" \
  "registry, so the file's provider address is rewritten once with" \
  "Terraform's own command. Stock's first plan back may propose only one" \
  "kind of change: removing the label and the annotation, the only things" \
  "choudoufu ever added to an object."
cmd "cp ../live/.terraform/choudoufu-cache.tfstate terraform.tfstate ; terraform state replace-provider ... ; terraform plan"
cp "$CACHE" "$STOCK/terraform.tfstate"
RP="$(cd "$STOCK" && terraform state replace-provider -auto-approve registry.opentofu.org/hashicorp/kubernetes registry.terraform.io/hashicorp/kubernetes 2>&1)" \
  || fail "k8s-roundtrip" "terraform could not rewrite the handed-back file's provider address: $RP"
POUT="$(cd "$STOCK" && terraform plan -input=false -no-color 2>&1)" || fail "k8s-roundtrip" "stock refused the handed-back state: $POUT"
grep -E 'Plan: |No changes.' <<< "$POUT" | head -1 | evidence
grep -qE 'Plan: 0 to add, [0-9]+ to change, 0 to destroy|No changes.' <<< "$POUT" \
  || fail "k8s-roundtrip" "the exit plan proposes more than removing the markers: $POUT"
if ! grep -q "No changes." <<< "$POUT"; then
  # Every changed line in the diff must be the label or the annotation.
  # Only the diff is read: the symbol legend above it ("~ update in-place")
  # has the shape of a changed line and is not one.
  DIFF="$(sed -n '/will perform the following actions:/,$p' <<< "$POUT")"
  grep -q 'tofu-estate' <<< "$DIFF" \
    || fail "k8s-roundtrip" "the exit plan changes something, but no diff removing tofu-estate was found in it, so the check below would read nothing: $POUT"
  OTHER="$(grep -E '^ +[-+~] ' <<< "$DIFF" | grep -vE 'tofu-estate|tofu-address|^ +[-+~] (resource|metadata|labels|annotations)( |$)' || true)"
  [ -z "$OTHER" ] || fail "k8s-roundtrip" "the exit plan changes something other than the label and the annotation: $OTHER"
  SA="$(cd "$STOCK" && terraform apply -auto-approve -input=false -no-color 2>&1)" \
    || fail "k8s-roundtrip" "the marker-removal apply failed: $SA"
  P2="$(cd "$STOCK" && terraform plan -input=false -no-color 2>&1)" || fail "k8s-roundtrip" "the stock replan failed: $P2"
  grep -q "No changes." <<< "$P2" || fail "k8s-roundtrip" "stock is not converged after removing the markers: $P2"
fi
proof "stock accepted the file and owns the estate again."

step "5. teardown - by stock, from the handed-back file"
cmd "terraform apply -destroy -auto-approve"
DOUT="$(cd "$STOCK" && terraform apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-roundtrip" "stock destroy failed: $DOUT"
grep -qE "Resources: 0 added, 0 changed, 4 destroyed|Destroy complete! Resources: 4 destroyed" <<< "$DOUT" \
  || fail "k8s-roundtrip" "stock did not destroy all 4 objects: $DOUT"
if kc get namespace "$NS" >/dev/null 2>&1; then
  PHASE="$(kc get namespace "$NS" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
  [ "$PHASE" = "Terminating" ] || fail "k8s-roundtrip" "the namespace is still there after stock's destroy (phase '$PHASE')"
fi
proof "4 destroyed by stock alone, from the file choudoufu handed back."

echo "  What you watched: a stock estate on a real cluster adopted with one"
echo "  command, operated with its state file deleted, and handed back as a"
echo "  state file stock could plan, converge and destroy with."
