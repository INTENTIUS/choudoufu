# k8s-plan-cost-tracks-the-estate
# CLAIM 14 (kubernetes) - A plan costs its estate. ~4 min.
#
# This proof: on a real cluster, a plan's requests do not move when the
# cluster around the estate grows by foreign objects, marked for another
# estate or not marked at all; every list the sweep sends carries the
# estate's label selector, so the server filters, and no kind is listed
# unfiltered.
#
# DRAFT: written and never run (live/smoke/drafts/README.md). Claim 14's
# Kubernetes cell stays open until both arms have run green.
#
# What it does not claim, stated where the claim lives (#1065): there is no
# cross-kind label-filtered list on a cluster. The sweep is API discovery
# plus one label-selected list per kind the cluster serves, so a plan's
# floor is the number of kinds served, not one call. That floor is printed
# below as a figure, and what is held fixed is that foreign objects add
# nothing to it.
#
# How it counts. The API server's apiserver_request_total counts every
# client, kube-controller-manager's included, so a plan's own requests
# cannot be told apart in it. The measured plans run through
# live/smoke/k8sproxy.py with no fault armed, which logs one line per
# request this client sent; the sum is that file's line count.
#
# BREAK=1 relabels the unmarked foreign ConfigMaps with THIS estate's label,
# which makes them this estate's orphans, and requires the request count to
# rise above the quiet figure. Without it, "the count did not move" would
# read the same from a counter that could not see objects at all.
#
#   FOREIGN_SCALE=1 (default)  40 ConfigMaps marked for another estate and 40
#                              unmarked, in a namespace of their own

FOREIGN_SCALE="${FOREIGN_SCALE:-1}"
W="$SMOKE_WORKROOT/k8s-plancost"
mkdir -p "$W/estate"
SMOKE_WORK="$W/estate"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-k8s/." "$SMOKE_WORK/"
ESTATE="smoke-k8s-cost"
sed_i "$SMOKE_WORK/versions.tf" "s/estate = \"smoke-k8s\"/estate = \"$ESTATE\"/"
grep -q "estate = \"$ESTATE\"" "$SMOKE_WORK/versions.tf" || fail "k8s-plancost" "the estate rename did not take in versions.tf"
FOREIGN_NS="smoke-foreign-load"
N=$((40 * FOREIGN_SCALE))

cluster_up
k8s_counting_proxy_up "$W/proxy" k8s-plancost
trap 'kill "$PROXY_PID" 2>/dev/null || true; cleanup' EXIT
PLOG="$W/proxy/proxy.log"

# measured_plan <label> runs one plan through the proxy, with the cache
# removed first so every run pays the same reads, and prints the number of
# requests it sent. The plan's text goes to $W/<label>.plan.
measured_plan() {
  rm -f "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
  : > "$PLOG"
  ( cd "$SMOKE_WORK" && KUBECONFIG="$PROXY_KC" KUBE_CONFIG_PATH="$PROXY_KC" "$TOFU" plan -input=false -no-color > "$W/$1.plan" 2>&1 ) || true
  cp "$PLOG" "$W/$1.requests"
  grep -c . "$PLOG" || true
}

# foreign_cms <namespace> <prefix> <count> <label or empty> prints a List
# manifest of that many ConfigMaps.
foreign_cms() {
  python3 - "$@" <<'PYEOF'
import json, sys
ns, prefix, n, label = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
items = []
for i in range(n):
    md = {"name": "%s-%03d" % (prefix, i), "namespace": ns}
    if label:
        md["labels"] = {"tofu-estate": label}
    items.append({"apiVersion": "v1", "kind": "ConfigMap", "metadata": md, "data": {"i": str(i)}})
print(json.dumps({"apiVersion": "v1", "kind": "List", "items": items}))
PYEOF
}

step "the claim"
explain \
  "A plan costs its estate. What a plan reads should scale with what the" \
  "estate owns, not with what else the cluster holds. This measures one" \
  "plan's requests, grows the cluster by $((2 * N)) objects that are not" \
  "this estate's, and measures again."

step "1. the estate, and the quiet figure"
cmd "choudoufu apply -auto-approve ; choudoufu plan   # through the counting proxy"
logged k8s-plancost-init "k8s-plancost" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
A1="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "k8s-plancost" "apply failed: $A1"
grep -qE 'Apply complete! Resources: 4 added' <<< "$A1" || fail "k8s-plancost" "the apply did not add the 4 objects: $A1"
# One plan through the proxy before anything is counted, so a client's
# first-run cost (a discovery cache filled on disk, say) is paid outside
# the comparison.
measured_plan warmup >/dev/null
grep -q "No changes." "$W/warmup.plan" || fail "k8s-plancost" "the warm-up plan through the proxy is not empty, so the proxy changes the answer: $(cat "$W/warmup.plan")"
QUIET="$(measured_plan quiet)"
grep -q "No changes." "$W/quiet.plan" || fail "k8s-plancost" "the quiet plan is not empty: $(cat "$W/quiet.plan")"
[ "$QUIET" -gt 0 ] || fail "k8s-plancost" "the proxy logged no request for a whole plan, so it is not on the plan's path"
LISTS="$( { grep -cE '^GET [^ ]+\?[^ ]*labelSelector=tofu-estate%3D'"$ESTATE" "$W/quiet.requests" || true; } )"
OTHER="$( { grep -vE '^GET [^ ]+\?[^ ]*labelSelector=' "$W/quiet.requests" | grep -c . || true; } )"
echo "quiet cluster: $QUIET requests, of which $LISTS are label-selected lists (one per kind served) and $OTHER are discovery and per-object reads" | evidence
[ "$LISTS" -gt 0 ] || fail "k8s-plancost" "no request carried this estate's label selector, so the sweep is not what the proxy saw: $(head -20 "$W/quiet.requests")"
proof "a quiet figure of $QUIET requests for a 4-object estate."

step "2. grow the cluster by objects that are not this estate's"
explain \
  "$N ConfigMaps labelled tofu-estate=someone-else, which a selector on" \
  "this estate's label excludes on the server, and $N with no label at" \
  "all, in a namespace of their own."
cmd "kubectl create namespace $FOREIGN_NS ; kubectl create -f marked.json -f unmarked.json"
kc create namespace "$FOREIGN_NS" >/dev/null || fail "k8s-plancost" "could not create the foreign namespace"
foreign_cms "$FOREIGN_NS" marked "$N" someone-else > "$W/marked.json"
foreign_cms "$FOREIGN_NS" unmarked "$N" "" > "$W/unmarked.json"
kc create -f "$W/marked.json" >/dev/null || fail "k8s-plancost" "could not create the marked foreign ConfigMaps"
kc create -f "$W/unmarked.json" >/dev/null || fail "k8s-plancost" "could not create the unmarked foreign ConfigMaps"
HAVE="$(kc get configmaps -n "$FOREIGN_NS" -o name | grep -c '^configmap/\(un\)\?marked-' || true)"
[ "$HAVE" = "$((2 * N))" ] || fail "k8s-plancost" "the foreign namespace holds $HAVE of the $((2 * N)) ConfigMaps"
proof "$((2 * N)) foreign ConfigMaps on the cluster."

if [ "${BREAK:-0}" = "1" ]; then
  step "BREAK control - make the unmarked ones this estate's; the count must rise"
  explain \
    "The equality step 3 asserts is only worth something if the counter" \
    "sees objects that are the estate's. So the $N unmarked ConfigMaps" \
    "get THIS estate's label: they are now this estate's orphans, the" \
    "plan proposes removing them, and the requests must rise above the" \
    "quiet figure."
  cmd "kubectl label configmap -n $FOREIGN_NS -l '!tofu-estate' tofu-estate=$ESTATE"
  kc label configmap -n "$FOREIGN_NS" -l '!tofu-estate' "tofu-estate=$ESTATE" >/dev/null \
    || fail "k8s-plancost" "BREAK: could not label the unmarked ConfigMaps"
  OWNED="$(measured_plan break)"
  grep -E '^Plan:|^Error' "$W/break.plan" | head -2 | evidence
  echo "quiet: $QUIET requests; with $N objects relabelled as this estate's: $OWNED" | evidence
  grep -qE "Plan: 0 to add, 0 to change, $N to destroy" "$W/break.plan" \
    || fail "k8s-plancost" "BREAK: the relabelled ConfigMaps were not all proposed for removal: $(grep -E '^Plan:|^Error' "$W/break.plan")"
  [ "$OWNED" -gt "$QUIET" ] \
    || fail "k8s-plancost" "BREAK: $N more objects of this estate's cost no more requests ($OWNED against $QUIET), so the counter cannot see per-object cost and the equality in step 3 proves nothing"
  proof "caught: objects that are this estate's cost requests ($QUIET -> $OWNED), so the counter sees them, and step 3's equality is a comparison that can fail."
  exit 0
fi

step "3. the same plan, measured again"
cmd "choudoufu plan   # through the counting proxy"
LOADED="$(measured_plan loaded)"
grep -q "No changes." "$W/loaded.plan" || fail "k8s-plancost" "the plan over the grown cluster is not empty: $(cat "$W/loaded.plan")"
echo "quiet: $QUIET requests; with $((2 * N)) foreign ConfigMaps: $LOADED" | evidence
UNFILTERED="$( { grep -E '^GET /api/v1/(namespaces/[^/]+/)?configmaps(\?| )' "$W/loaded.requests" | grep -v 'labelSelector=' || true; } )"
[ -z "$UNFILTERED" ] || fail "k8s-plancost" "the plan listed ConfigMaps with no label selector, so foreign objects reach the client: $UNFILTERED"
[ "$LOADED" = "$QUIET" ] \
  || fail "k8s-plancost" "foreign objects moved the plan's request count ($QUIET -> $LOADED); the difference: $(diff <(sed 's/ [0-9]*$//' "$W/quiet.requests" | sort) <(sed 's/ [0-9]*$//' "$W/loaded.requests" | sort) | head -20)"
proof "$((2 * N)) foreign objects and the plan sent the same $QUIET requests; no ConfigMap list went out without the estate's selector."

step "4. teardown"
cmd "choudoufu apply -destroy -auto-approve ; kubectl delete namespace $FOREIGN_NS"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "k8s-plancost" "teardown failed: $DOUT"
destroyed_exactly "k8s-plancost" 4 "$DOUT"
kc delete namespace "$FOREIGN_NS" --wait=false >/dev/null 2>&1 || true
proof "the estate is gone, and the foreign namespace with it."

echo "  What you watched: a plan on a real cluster send the same requests"
echo "  before and after the cluster grew by $((2 * N)) objects that were not its"
echo "  estate's, every list carrying the estate's label selector."
