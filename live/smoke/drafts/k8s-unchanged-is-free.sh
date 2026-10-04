# k8s-unchanged-is-free
# CLAIM 9 (kubernetes) - Unchanged is free. ~3 min.
#
# This proof: re-planning an unchanged Kubernetes estate with -refresh=false
# serves the objects the sweep vouched for from the cache and skips their
# reads, measured on the wire; reads = "full" (CHOUDOUFU_READS=full) turns
# that off and pays every read; and the two plans print the same answer.
#
# DRAFT: written and never run (live/smoke/drafts/README.md), and by reading
# the code its step 2 is expected to FAIL today. Its first run is the
# measurement that settles it. The cache hit needs a vouch
# (projection.cacheHit: Ownership.Verified, fed by discovery's
# Result.MarkerVerified, or a record envelope). The Kubernetes sweep joins an
# object declared by its kind and natural key and records no vouch for it
# (internal/live/discovery/kubernetes.go skips declared objects; only
# address-annotation bindings reach Result.Bindings). The other route, #692's
# cache-vouch listing (discovery.go, req.CacheVouchTypes), lists a type
# through the provider's list schema, and whether hashicorp/kubernetes 3.2.1
# answers one for these types was not established by reading. If step 2
# reports zero hits, the claim does not hold on Kubernetes and the fix is a
# vouch from the Kubernetes sweep, not this scenario.
#
# The AWS proof's record-backed half (a local record store's record is the
# attestation) touches no cloud and no cluster, so it is not repeated here.
#
# BREAK=1 deletes the cache before the selective plan and requires it to
# serve nothing and cost what the full plan costs, so the "fewer requests"
# comparison is shown able to fail.

W="$SMOKE_WORKROOT/k8s-unchanged"
mkdir -p "$W/estate"
SMOKE_WORK="$W/estate"; export SMOKE_WORK
cp -R "$ROOT/live/e2e/estate-k8s/." "$SMOKE_WORK/"
ESTATE="smoke-k8s-unchanged"
sed_i "$SMOKE_WORK/versions.tf" "s/estate = \"smoke-k8s\"/estate = \"$ESTATE\"/"
grep -q "estate = \"$ESTATE\"" "$SMOKE_WORK/versions.tf" || fail "k8s-unchanged" "the estate rename did not take in versions.tf"
CACHE="$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"

cluster_up
k8s_counting_proxy_up "$W/proxy" k8s-unchanged
trap 'kill "$PROXY_PID" 2>/dev/null || true; cleanup' EXIT
PLOG="$W/proxy/proxy.log"

# refresh_false_plan <label> [env...] runs plan -refresh=false through the
# proxy with a debug log, and prints "<cache hits> <requests>". The plan's
# text is $W/<label>.plan.
refresh_false_plan() {
  local label="$1"; shift
  : > "$PLOG"
  ( cd "$SMOKE_WORK" && env "$@" KUBECONFIG="$PROXY_KC" KUBE_CONFIG_PATH="$PROXY_KC" TF_LOG=debug TF_LOG_PATH="$W/$label.log" \
      "$TOFU" plan -refresh=false -input=false -no-color 2>&1 | grep -v '^discovering:' > "$W/$label.plan" ) || true
  printf '%s %s\n' "$( { grep -c 'state cache hit' "$W/$label.log" || true; } )" "$( { grep -c . "$PLOG" || true; } )"
}

step "the claim"
explain \
  "Unchanged is free. On the -refresh=false path an object the run can" \
  "vouch for is served from the cache and its read is never sent. One" \
  "argument, reads = \"full\" (or CHOUDOUFU_READS=full for one run)," \
  "turns that off, and it may change the price of a plan, never its" \
  "answer."

step "1. stand the estate up"
cmd "choudoufu apply -auto-approve"
logged k8s-unchanged-init "k8s-unchanged" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
A1="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "k8s-unchanged" "apply failed: $A1"
grep -qE 'Apply complete! Resources: 4 added' <<< "$A1" || fail "k8s-unchanged" "the apply did not add the 4 objects: $A1"
[ -f "$CACHE" ] || fail "k8s-unchanged" "no cache after the apply"
cp "$CACHE" "$W/cache.keep"
proof "an estate up and a fresh cache beside it."

step "2. the free re-plan, and the argument that refuses it"
cmd "choudoufu plan -refresh=false ; CHOUDOUFU_READS=full choudoufu plan -refresh=false   # both through the proxy"
if [ "${BREAK:-0}" = "1" ]; then
  explain \
    "BREAK control: the cache is deleted before the selective plan. With" \
    "nothing to serve from, it must serve nothing and cost what the full" \
    "plan costs; if it still reads as cheaper, the comparison below is" \
    "not measuring the cache."
  rm -f "$CACHE"
fi
read -r HITS_SEL REQ_SEL <<< "$(refresh_false_plan selective)"
cp "$W/cache.keep" "$CACHE"
read -r HITS_FULL REQ_FULL <<< "$(refresh_false_plan full CHOUDOUFU_READS=full)"
echo "selective: $HITS_SEL served, $REQ_SEL requests; full: $HITS_FULL served, $REQ_FULL requests" | evidence
grep -q "No changes." "$W/full.plan" || fail "k8s-unchanged" "the full plan is not empty: $(cat "$W/full.plan")"
[ "$REQ_FULL" -gt 0 ] || fail "k8s-unchanged" "the full plan sent no request through the proxy, so the counter is not on its path"
[ "$HITS_FULL" = "0" ] || fail "k8s-unchanged" "reads=full still served $HITS_FULL from the cache - the off switch does not switch off"
if [ "${BREAK:-0}" = "1" ]; then
  [ "$HITS_SEL" = "0" ] || fail "k8s-unchanged" "BREAK: with no cache file the selective plan reports $HITS_SEL cache hits - the counter is lying"
  [ "$REQ_SEL" -ge "$REQ_FULL" ] || fail "k8s-unchanged" "BREAK: with no cache the selective plan still cost less ($REQ_SEL against $REQ_FULL), so the saving is not the cache's"
  proof "caught: with the cache gone the selective plan served nothing and paid the full price, so the saving the main arm measures is the cache's."
  exit 0
fi
[ "$HITS_SEL" -gt 0 ] || fail "k8s-unchanged" "the selective plan served nothing from a fresh cache: unchanged was not free on Kubernetes. Read this scenario's header: the Kubernetes sweep records no vouch for an object it joins by natural key"
[ "$REQ_SEL" -lt "$REQ_FULL" ] || fail "k8s-unchanged" "the selective plan saved no requests ($REQ_SEL against $REQ_FULL)"
diff "$W/selective.plan" "$W/full.plan" > "$W/plan-diff.txt" \
  || fail "k8s-unchanged" "the toggle changed the plan's answer, and it may only change the price: $(cat "$W/plan-diff.txt")"
proof "unchanged cost $REQ_SEL requests instead of $REQ_FULL, and the off switch restored the full bill without moving the answer."

step "3. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "k8s-unchanged" "teardown failed: $DOUT"
destroyed_exactly "k8s-unchanged" 4 "$DOUT"
proof "4 destroyed."

echo "  What you watched: an unchanged Kubernetes estate re-planned from the"
echo "  cache with its reads not sent, and one argument restoring every read"
echo "  without moving the answer."
