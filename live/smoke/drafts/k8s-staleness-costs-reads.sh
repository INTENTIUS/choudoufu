# k8s-staleness-costs-reads
# CLAIM 3 (kubernetes) - The cache never changes an answer. ~3 min.
#
# This proof: a cache that remembers a dead world, a cache that remembers an
# object that exists nowhere, and no cache at all each give the plan a fresh
# cache gives, byte for byte, on a real cluster; and a fresh cache does not
# hide a field edited behind the run's back.
#
# DRAFT: written and never run (live/smoke/drafts/README.md). Claim 3's
# Kubernetes cell stays open until both arms have run green.
#
# What is not here, and why. The AWS proof's step 4 measures the one path
# that serves a result from the cache, plan -refresh=false, and counts the
# reads it saves. On Kubernetes that path serves nothing today: the cache
# hit needs the estate sweep to have vouched for the instance
# (projection.cacheHit, Ownership.Verified), and the Kubernetes sweep joins
# an object declared by its natural key without recording it as verified
# (internal/live/discovery/kubernetes.go skips declared objects; only
# address-annotation bindings reach Result.Bindings). That gap is claim 9's,
# and its cell says so. What this claim promises, that no cache state changes
# the answer, does not depend on it.
#
# The AWS proof's record-store half (a phantom terraform_data in an ancient
# cache) becomes a phantom ConfigMap here: there is no record-only
# Kubernetes type (#1441), and a ConfigMap the cache remembers and the
# cluster does not hold is the same test of whether the cache has any
# authority.
#
# BREAK=1 edits the ConfigMap's data with kubectl and requires the plan to
# differ from the fresh-cache plan, so the byte comparisons above it are
# shown able to fail.

SMOKE_WORK="$SMOKE_WORKROOT/k8s-staleness"
mkdir -p "$SMOKE_WORK"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-k8s/." "$SMOKE_WORK/"
sed_i "$SMOKE_WORK/versions.tf" 's/estate = "smoke-k8s"/estate = "smoke-k8s-stale"/'
grep -q 'estate = "smoke-k8s-stale"' "$SMOKE_WORK/versions.tf" \
  || fail "k8s-stale" "the estate rename did not take in versions.tf"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
ANCIENT="$SMOKE_WORK/ancient-cache.tfstate"

# plan_filtered is the plan's text minus the sweep's progress lines, which
# carry timings and are not part of the answer.
plan_filtered() { (cd "$SMOKE_WORK" && chdf plan -input=false -no-color 2>&1 | grep -v '^discovering:'); }
# cached_uid prints the metadata.uid a cache file remembers for one block.
cached_uid() { # <cache file> <type> <name>
  python3 - "$1" "$2" "$3" <<'PYEOF'
import json, sys
d = json.load(open(sys.argv[1]))
for r in d.get("resources", []):
    if r.get("type") == sys.argv[2] and r.get("name") == sys.argv[3] and r.get("mode") == "managed":
        for i in r.get("instances", []):
            print(i["attributes"]["metadata"][0]["uid"])
            sys.exit(0)
print("")
PYEOF
}

cluster_up

step "the claim"
explain \
  "Staleness costs reads, never results. The state cache is a memo of" \
  "the last projection: ownership is never read from it, live wins any" \
  "disagreement, and losing it costs a slower run. This puts the cache in" \
  "every condition it can reach on a cluster and requires one identical" \
  "plan each time."

step "1. stand up, and manufacture an ancient cache"
explain \
  "The first apply creates the estate plus one extra ConfigMap, phantom," \
  "and writes cache C1. The whole estate is then destroyed, the phantom's" \
  "block is deleted, and the rest is applied again. Every object is new" \
  "(the API server assigns a fresh metadata.uid), and the phantom exists" \
  "nowhere. C1 is kept aside: it remembers four uids that are dead and" \
  "one object that is gone."
cat > "$SMOKE_WORK/phantom.tf" <<'TFEOF'
resource "kubernetes_config_map" "phantom" {
  metadata {
    name      = "phantom-config"
    namespace = "smoke-k8s"
  }
  data = { remembered = "only by the cache" }

  depends_on = [kubernetes_namespace.app]
}
TFEOF
cmd "apply ; save the cache ; apply -destroy ; rm phantom.tf ; apply"
logged k8s-staleness-init "k8s-stale" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
A1="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "k8s-stale" "the first apply failed: $A1"
grep -qE 'Apply complete! Resources: 5 added' <<< "$A1" || fail "k8s-stale" "the first apply did not add the 5 objects: $A1"
[ -f "$CACHE" ] || fail "k8s-stale" "no cache after the first apply"
cp "$CACHE" "$ANCIENT"
[ -n "$(cached_uid "$ANCIENT" kubernetes_config_map phantom)" ] \
  || fail "k8s-stale" "the saved cache does not remember the phantom, so the phantom test below would test nothing"
D1="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "k8s-stale" "the destroy failed: $D1"
destroyed_exactly "k8s-stale" 5 "$D1"
rm -f "$SMOKE_WORK/phantom.tf"
A2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "k8s-stale" "the second apply failed: $A2"
grep -qE 'Apply complete! Resources: 4 added' <<< "$A2" || fail "k8s-stale" "the second apply did not add the 4 objects: $A2"
OLD_UID="$(cached_uid "$ANCIENT" kubernetes_config_map app)"
NEW_UID="$(kc get configmap app-config -n smoke-k8s -o jsonpath='{.metadata.uid}')"
echo "the ancient cache remembers ConfigMap uid $OLD_UID; the cluster holds $NEW_UID" | evidence
[ -n "$OLD_UID" ] && [ -n "$NEW_UID" ] || fail "k8s-stale" "could not read both uids (cache '$OLD_UID', cluster '$NEW_UID')"
[ "$OLD_UID" != "$NEW_UID" ] || fail "k8s-stale" "the recreated ConfigMap has the uid the cache remembers, so the staleness is not real"
if kc get configmap phantom-config -n smoke-k8s >/dev/null 2>&1; then
  fail "k8s-stale" "phantom-config still exists after the destroy"
fi
proof "the ancient cache and the cluster disagree about every object's uid, and the cache remembers a ConfigMap the cluster does not hold."

step "2. three cache states, one answer"
explain \
  "The same plan under the fresh cache, under the ancient one, and with" \
  "no cache file at all. The three outputs must be byte-identical, and" \
  "the phantom must appear in none of them: not as a destroy, not as" \
  "anything. A cache that could bend a plan toward its memory would be a" \
  "record, and the cache is never allowed to be one."
cmd "plan (fresh) ; plan (ancient) ; plan (absent)"
P_FRESH="$(plan_filtered)" || fail "k8s-stale" "the fresh-cache plan failed: $P_FRESH"
grep -q "No changes." <<< "$P_FRESH" || fail "k8s-stale" "the converged estate does not plan clean: $P_FRESH"
cp "$ANCIENT" "$CACHE"
P_ANCIENT="$(plan_filtered)" || fail "k8s-stale" "the ancient-cache plan failed: $P_ANCIENT"
rm -f "$CACHE"
P_ABSENT="$(plan_filtered)" || fail "k8s-stale" "the absent-cache plan failed: $P_ABSENT"
[ "$P_FRESH" = "$P_ANCIENT" ] || fail "k8s-stale" "the ancient cache changed the plan.
--- fresh ---
$P_FRESH
--- ancient ---
$P_ANCIENT"
[ "$P_FRESH" = "$P_ABSENT" ] || fail "k8s-stale" "the missing cache changed the plan.
--- fresh ---
$P_FRESH
--- absent ---
$P_ABSENT"
if grep -q 'phantom' <<< "$P_ANCIENT"; then
  fail "k8s-stale" "the phantom leaked out of the ancient cache into the plan: $P_ANCIENT"
fi
grep -E 'No changes\.' <<< "$P_ANCIENT" | head -1 | evidence
proof "fresh, ancient and absent gave one identical plan, and the phantom the ancient cache remembers is in none of them."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - move the cluster; the comparison must notice"
  explain \
    "Three identical outputs prove nothing if the comparison cannot fail." \
    "This edits the ConfigMap's data with kubectl and plans again. The" \
    "plan must differ from the fresh one now, or the equality above was" \
    "comparing outputs that could never differ."
  cmd "kubectl patch configmap app-config -n smoke-k8s -p '{\"data\":{\"greeting\":\"drifted\"}}' ; choudoufu plan"
  kc patch configmap app-config -n smoke-k8s --type merge -p '{"data":{"greeting":"drifted"}}' >/dev/null \
    || fail "k8s-stale" "BREAK: could not edit the ConfigMap"
  P_DRIFT="$(plan_filtered || true)"
  grep -E '^Plan:|will be updated' <<< "$P_DRIFT" | head -2 | evidence
  if [ "$P_FRESH" = "$P_DRIFT" ]; then
    fail "k8s-stale" "BREAK: the cluster moved and the plan text did not, so the equality checks above could not fail"
  fi
  grep -q 'kubernetes_config_map.app will be updated in-place' <<< "$P_DRIFT" \
    || fail "k8s-stale" "BREAK: the plan differs, but not by the edit made: $P_DRIFT"
  proof "caught: a real edit moved the plan, so the three-way equality is a comparison that can fail."
  exit 0
fi

step "3. a field edited behind the run's back, under a fresh cache"
explain \
  "The AWS form of this step is a regression that shipped: a fresh cache" \
  "served an instance's attributes and an out-of-band edit went unseen" \
  "(#712). A default plan reads every object. So: a no-op apply to put a" \
  "fresh cache back, an edit to the ConfigMap's data with kubectl, and" \
  "the plan must show it."
cmd "choudoufu apply ; kubectl patch configmap app-config ... ; choudoufu plan"
A3="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "k8s-stale" "the no-op apply failed: $A3"
grep -qE 'Resources: 0 added, 0 changed, 0 destroyed' <<< "$A3" || fail "k8s-stale" "the no-op apply was not a no-op: $A3"
[ -f "$CACHE" ] || fail "k8s-stale" "the apply wrote no cache, so this step would not be testing a fresh one"
kc patch configmap app-config -n smoke-k8s --type merge -p '{"data":{"greeting":"drifted"}}' >/dev/null \
  || fail "k8s-stale" "could not edit the ConfigMap"
P_DRIFT="$(plan_filtered)" || fail "k8s-stale" "the plan after the edit failed: $P_DRIFT"
grep -E 'greeting|^Plan:' <<< "$P_DRIFT" | head -3 | evidence
grep -qE 'Plan: 0 to add, 1 to change, 0 to destroy' <<< "$P_DRIFT" \
  || fail "k8s-stale" "the edit is not visible past a fresh cache: $P_DRIFT"
grep -q 'kubernetes_config_map.app will be updated in-place' <<< "$P_DRIFT" \
  || fail "k8s-stale" "the one change proposed is not the ConfigMap's: $P_DRIFT"
A4="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "k8s-stale" "the reconverging apply failed: $A4"
grep -qE 'Resources: 0 added, 1 changed, 0 destroyed' <<< "$A4" \
  || fail "k8s-stale" "reconvergence was not exactly one in-place change: $A4"
GREETING="$(kc get configmap app-config -n smoke-k8s -o jsonpath='{.data.greeting}')"
[ "$GREETING" = "hello" ] || fail "k8s-stale" "after the apply the ConfigMap reads greeting=$GREETING, want hello"
proof "the read is drift detection and a fresh cache does not excuse skipping it; one apply put the declared value back."

step "4. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" \
  || fail "k8s-stale" "teardown failed: $DOUT"
destroyed_exactly "k8s-stale" 4 "$DOUT"
proof "4 destroyed; the estate is gone."

echo "  What you watched: a cache holding dead uids and a ConfigMap that"
echo "  exists nowhere, an absent cache, and a fresh one give one identical"
echo "  plan on a real cluster, and an edit made behind the run's back show"
echo "  straight through a fresh cache."
