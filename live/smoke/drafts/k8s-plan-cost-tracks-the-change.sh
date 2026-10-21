# k8s-plan-cost-tracks-the-change
# CLAIM 47 (kubernetes) - A plan costs its change. ~4 min.
#
# This proof: on a real cluster, `choudoufu plan -cone=HEAD` over a change
# to one ConfigMap reads that ConfigMap and the one block that reads it,
# not the estate. Its request count is the same figure when the estate
# around the change grows from 20 ConfigMaps to 80, while a full plan's
# grows with the estate, and its changes are the full plan's changes.
#
# DRAFT: written for GitHub issue #2077 and never run
# (live/smoke/drafts/README.md). Number 47 is reserved for it until both
# arms run green and this is promoted to scenarios/.
#
# What it does not claim (live/PLAN-CONE.md): the marker sweep stays
# estate-wide, because it is the orphan check and the cone's sweep guard.
# On a cluster that sweep is API discovery plus one label-selected list
# per kind served (#1065), and its cost does not depend on how many
# objects the estate holds. What the cone removes is the per-object read
# of every block outside it. Drift outside the cone is not reported.
#
# How it counts. As in k8s-plan-cost-tracks-the-estate (claim 14): every
# measured plan runs through live/smoke/k8sproxy.py with no fault armed,
# which logs one line per request this client sent, and the cache is
# removed before each one so every plan pays the reads a fresh checkout
# would. A per-object read is a GET of one named ConfigMap in the estate's
# namespace; the lists the sweep sends have no name segment.
#
# BREAK=1 points -cone at a base that is not a commit. The guard then plans
# the whole estate with a "Cone plan fell back to a full plan" warning, and
# the control passes only when the read check below refuses that plan as a
# cone plan: every ConfigMap read, the full plan's request count. Without
# it, "the cone plan read two objects" would read the same from a check
# that could not see a full plan.
#
#   N1=20, N2=80 (defaults)  the estate's ConfigMaps before and after it grows

N1="${N1:-20}"
N2="${N2:-80}"
[ "$N2" -gt "$N1" ] || fail "k8s-cone" "N2 ($N2) must be larger than N1 ($N1)"
W="$SMOKE_WORKROOT/k8s-cone"
mkdir -p "$W/estate"
SMOKE_WORK="$W/estate"; export SMOKE_WORK
ESTATE="smoke-k8s-cone"
NS="smoke-k8s-cone"
cp "$ROOT/live/e2e/estate-k8s/versions.tf" "$SMOKE_WORK/"
sed_i "$SMOKE_WORK/versions.tf" "s/estate = \"smoke-k8s\"/estate = \"$ESTATE\"/"
grep -q "estate = \"$ESTATE\"" "$SMOKE_WORK/versions.tf" || fail "k8s-cone" "the estate rename did not take in versions.tf"
command -v git >/dev/null 2>&1 || fail "k8s-cone" "git is not installed; -cone diffs the configuration against a git commit"

# write_estate <count> <value of cm-000> writes main.tf: one namespace,
# <count> ConfigMaps each in a block of its own (a count would make them one
# block, and the cone is per block), and cm-reader, which reads cm-000's
# value. A change to cm-000 therefore has a cone of two blocks: cm-000 and
# the block that reads it.
write_estate() {
  python3 - "$1" "$2" "$NS" > "$SMOKE_WORK/main.tf" <<'PYEOF'
import sys
n, first, ns = int(sys.argv[1]), sys.argv[2], sys.argv[3]
out = ['resource "kubernetes_namespace" "app" {\n  metadata {\n    name = "%s"\n  }\n}\n' % ns]
for i in range(n):
    value = first if i == 0 else "v1"
    out.append(
        'resource "kubernetes_config_map" "cm_%03d" {\n'
        '  metadata {\n    name      = "cm-%03d"\n    namespace = kubernetes_namespace.app.metadata[0].name\n  }\n'
        '  data = {\n    value = "%s"\n  }\n}\n' % (i, i, value))
out.append(
    'resource "kubernetes_config_map" "cm_reader" {\n'
    '  metadata {\n    name      = "cm-reader"\n    namespace = kubernetes_namespace.app.metadata[0].name\n  }\n'
    '  data = {\n    from = kubernetes_config_map.cm_000.data["value"]\n  }\n}\n')
print("\n".join(out), end="")
PYEOF
}

# commit_estate records the working tree as HEAD, the cone's base.
commit_estate() {
  git -C "$SMOKE_WORK" add -A >/dev/null
  git -C "$SMOKE_WORK" -c user.name=smoke -c user.email=smoke@localhost commit -q -m "$1" \
    || fail "k8s-cone" "git commit failed in the estate directory"
}

cluster_up
k8s_counting_proxy_up "$W/proxy" k8s-cone
trap 'kill "$PROXY_PID" 2>/dev/null || true; cleanup' EXIT
PLOG="$W/proxy/proxy.log"

# measured_plan <label> [plan flags] runs one plan through the proxy, with
# the cache removed first, and prints the number of requests it sent. The
# plan's text goes to $W/<label>.plan and its request log to
# $W/<label>.requests.
measured_plan() {
  local label="$1"; shift
  rm -f "$SMOKE_WORK/.terraform/choudoufu-cache.tfstate"
  : > "$PLOG"
  ( cd "$SMOKE_WORK" && KUBECONFIG="$PROXY_KC" KUBE_CONFIG_PATH="$PROXY_KC" "$TOFU" plan -input=false -no-color "$@" > "$W/$label.plan" 2>&1 ) || true
  cp "$PLOG" "$W/$label.requests"
  grep -c . "$PLOG" || true
}

# objects_read <label> prints, sorted and once each, the ConfigMaps in the
# estate's namespace that plan read by name.
objects_read() {
  { grep -oE '^GET /api/v1/namespaces/'"$NS"'/configmaps/[^ ?/]+' "$W/$1.requests" || true; } \
    | sed 's#.*/##' | sort -u
}

# planned_addrs <label> prints the addresses the plan proposes to change.
planned_addrs() {
  { grep -oE '^  # [^ ]+ will be [a-z -]+' "$W/$1.plan" || true; } | sed 's/^  # //' | sort
}

# cone_reads_only_the_cone <label> succeeds when that plan read cm-000 and
# cm-reader by name and no other ConfigMap. It is the check the main arm
# holds the cone plan to and the check BREAK=1 must see fail.
CONE_SET="$(printf 'cm-000\ncm-reader\n')"
cone_reads_only_the_cone() {
  [ "$(objects_read "$1")" = "$CONE_SET" ]
}

# measure_change <n> <tag> plans the change to cm-000 in full and as a cone
# over an estate of <n> ConfigMaps, asserts both, and sets LAST_FULL and
# LAST_CONE to their request counts. <tag> names the plans' files.
measure_change() {
  local n="$1" tag="$2" full cone_n reads
  full="$(measured_plan "full-$tag")"
  grep -qE '^Plan: 0 to add, 2 to change, 0 to destroy' "$W/full-$tag.plan" \
    || fail "k8s-cone" "the full plan at $n ConfigMaps does not propose the two updates (cm-000 and the block that reads it): $(grep -E '^Plan:|^Error|^No changes' "$W/full-$tag.plan")"
  reads="$(objects_read "full-$tag" | grep -c . || true)"
  [ "$reads" = "$((n + 1))" ] \
    || fail "k8s-cone" "the full plan at $n ConfigMaps read $reads of the estate's $((n + 1)) ConfigMaps by name, so a per-object read is not what this counter sees: $(head -20 "$W/full-$tag.requests")"

  cone_n="$(measured_plan "cone-$tag" "-cone=$CONE_BASE")"
  grep -E '^Plan:|Cone plan|covers' "$W/cone-$tag.plan" | head -4 | evidence
  echo "estate of $n ConfigMaps: full plan $full requests ($reads ConfigMaps read by name), cone plan $cone_n requests ($(objects_read "cone-$tag" | tr '\n' ' '))" | evidence

  if [ "${BREAK:-0}" = "1" ]; then
    step "BREAK control - -cone at a base that is not a commit; the read check must refuse it"
    explain \
      "The guard cannot diff against a base that is not a commit, so it" \
      "plans the whole estate and says so. The plan is still correct; what" \
      "must happen here is that the check step 2 holds a cone plan to sees" \
      "a full plan when it is handed one."
    grep -q "Cone plan fell back to a full plan" "$W/cone-$tag.plan" \
      || fail "k8s-cone" "BREAK: -cone=$CONE_BASE did not fall back with the guard's warning, so this control did not force a full plan: $(grep -iE 'cone|^Plan:|^Error' "$W/cone-$tag.plan")"
    if cone_reads_only_the_cone "cone-$tag"; then
      fail "k8s-cone" "BREAK: a plan that fell back to the whole estate read only $(objects_read "cone-$tag" | tr '\n' ' ')by name, so the read check cannot tell a full plan from a cone plan and step 2 proves nothing"
    fi
    [ "$(objects_read "cone-$tag" | grep -c . || true)" = "$((n + 1))" ] \
      || fail "k8s-cone" "BREAK: the fallen-back plan read $(objects_read "cone-$tag" | grep -c . || true) ConfigMaps by name, not the full plan's $((n + 1))"
    [ "$cone_n" -gt "$((full - n / 2))" ] \
      || fail "k8s-cone" "BREAK: the fallen-back plan sent $cone_n requests against the full plan's $full, so the counter does not see a full plan's cost"
    proof "caught: a cone plan that fell back read all $((n + 1)) ConfigMaps ($cone_n requests against the full plan's $full), and the read check refused it."
    exit 0
  fi

  grep -q "covers the cone of the change since $CONE_BASE: 2 of the estate's $((n + 2)) blocks" "$W/cone-$tag.plan" \
    || fail "k8s-cone" "the cone plan at $n ConfigMaps is not a cone of 2 of $((n + 2)) blocks: $(grep -iE 'cone|^Plan:|^Error' "$W/cone-$tag.plan")"
  [ "$(planned_addrs "cone-$tag")" = "$(planned_addrs "full-$tag")" ] \
    || fail "k8s-cone" "the cone plan's changes differ from the full plan's: $(diff <(planned_addrs "full-$tag") <(planned_addrs "cone-$tag"))"
  cone_reads_only_the_cone "cone-$tag" \
    || fail "k8s-cone" "the cone plan at $n ConfigMaps read $(objects_read "cone-$tag" | tr '\n' ' ')by name; a cone of cm-000 and cm-reader reads those two"
  [ "$cone_n" -lt "$full" ] \
    || fail "k8s-cone" "the cone plan sent $cone_n requests and the full plan $full; a cone that costs the estate is no saving"
  LAST_FULL="$full"
  LAST_CONE="$cone_n"
}

CONE_BASE="HEAD"
[ "${BREAK:-0}" = "1" ] && CONE_BASE="no-such-base"

step "the claim"
explain \
  "A plan costs its change. A pull request that edits one block should" \
  "plan that block and what reads it, and pay for those reads, not for" \
  "every object the estate holds. This plans one change in full and as a" \
  "cone over an estate of $N1 ConfigMaps, then over $N2."

step "1. an estate of $N1 ConfigMaps, committed"
cmd "choudoufu apply -auto-approve ; git commit"
write_estate "$N1" v1
printf '.terraform/\n' > "$SMOKE_WORK/.gitignore"
git -C "$SMOKE_WORK" init -q || fail "k8s-cone" "git init failed in the estate directory"
logged k8s-cone-init "k8s-cone" "init failed" -- in_dir "$SMOKE_WORK" chdf init -input=false -no-color
A1="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "k8s-cone" "apply failed: $A1"
grep -qE "Apply complete! Resources: $((N1 + 2)) added" <<< "$A1" || fail "k8s-cone" "the apply did not add the $((N1 + 2)) objects: $A1"
commit_estate "estate of $N1"
measured_plan warmup >/dev/null
grep -q "No changes." "$W/warmup.plan" || fail "k8s-cone" "the warm-up plan through the proxy is not empty, so the proxy changes the answer: $(cat "$W/warmup.plan")"
[ -s "$W/warmup.requests" ] || fail "k8s-cone" "the proxy logged no request for a whole plan, so it is not on the plan's path"
proof "$((N1 + 2)) objects applied and committed as the cone's base; the plan through the proxy is empty."

step "2. change cm-000, plan it in full and as a cone"
explain \
  "cm-000's value goes from v1 to v2 in the working tree. cm-reader reads" \
  "that value, so the change is two updates, and the cone is those two" \
  "blocks out of $((N1 + 2))."
cmd "choudoufu live-cone HEAD ; choudoufu plan ; choudoufu plan -cone=$CONE_BASE   # both plans through the counting proxy"
write_estate "$N1" v2
TARGETS="$(cd "$SMOKE_WORK" && "$TOFU" live-cone HEAD 2>"$W/live-cone.err")" \
  || fail "k8s-cone" "live-cone HEAD did not print a cone (exit 2 is the guard's full plan): $TARGETS $(cat "$W/live-cone.err")"
echo "$TARGETS" | evidence
[ "$(grep -v '^#' <<< "$TARGETS" | grep . | sort)" = "$(printf 'kubernetes_config_map.cm_000\nkubernetes_config_map.cm_reader\n')" ] \
  || fail "k8s-cone" "live-cone HEAD's targets are not cm_000 and cm_reader: $TARGETS"
measure_change "$N1" small
FULL_small="$LAST_FULL"; CONE_small="$LAST_CONE"
proof "at $N1 ConfigMaps the cone plan read 2 by name and sent $CONE_small requests; the full plan read $((N1 + 1)) and sent $FULL_small. Both propose the same two updates."

step "3. grow the estate to $N2 ConfigMaps, and plan the same change"
cmd "choudoufu apply -auto-approve ; git commit ; (the same edit to cm-000) ; choudoufu plan ; choudoufu plan -cone=HEAD"
write_estate "$N2" v1
A2="$(cd "$SMOKE_WORK" && chdf apply -auto-approve -input=false -no-color 2>&1)" || fail "k8s-cone" "the growing apply failed: $A2"
grep -qE "Apply complete! Resources: $((N2 - N1)) added, 0 changed, 0 destroyed" <<< "$A2" \
  || fail "k8s-cone" "the growing apply did not add exactly $((N2 - N1)) ConfigMaps: $A2"
commit_estate "estate of $N2"
write_estate "$N2" v2
measure_change "$N2" big
FULL_big="$LAST_FULL"; CONE_big="$LAST_CONE"
echo "cone plan: $CONE_small requests at $N1 ConfigMaps, $CONE_big at $N2; full plan: $FULL_small, $FULL_big" | evidence
[ "$CONE_big" = "$CONE_small" ] \
  || fail "k8s-cone" "the cone plan's request count moved with the estate ($CONE_small at $N1 ConfigMaps, $CONE_big at $N2), so it tracks the estate, not the change; the difference: $(diff <(sed 's/ [0-9]*$//' "$W/cone-small.requests" | sort) <(sed 's/ [0-9]*$//' "$W/cone-big.requests" | sort) | head -20)"
[ "$FULL_big" -ge "$((FULL_small + N2 - N1))" ] \
  || fail "k8s-cone" "the full plan went from $FULL_small to $FULL_big requests for $((N2 - N1)) more ConfigMaps, under one read each, so the full plan is not the comparison this claims"
proof "the cone plan sent the same $CONE_small requests at $N1 and $N2 ConfigMaps while the full plan went $FULL_small -> $FULL_big."

step "4. teardown"
cmd "choudoufu apply -destroy -auto-approve"
DOUT="$(cd "$SMOKE_WORK" && chdf apply -destroy -auto-approve -input=false -no-color 2>&1)" || fail "k8s-cone" "teardown failed: $DOUT"
destroyed_exactly "k8s-cone" "$((N2 + 2))" "$DOUT"
proof "the estate is gone."

echo "  What you watched: one change planned as a cone on a real cluster read"
echo "  the two blocks it touched and sent the same requests whether the estate"
echo "  held $N1 ConfigMaps or $N2, while the full plan's requests grew with it."
