# plan-cost-tracks-the-change
# CLAIM 47 (aws) - A plan costs its change. ~15 min.
#
# This proof: Scale: a cone plan (plan -cone=BASE) costs the cone of the
# change, not the estate, and its changes are a full plan's changes
# restricted to the cone (GitHub issue #1956).

# Not written yet. The steps, for whoever writes it: generate terralith-scale
# at 745 and at 10,069 resources with tools/terralith-gen, apply it, commit,
# change 100 instances' worth of blocks, then plan once with -cone=HEAD and
# once in full and count both runs' calls with the existing harness. PASS
# needs the cone plan's changes to equal the full plan's restricted to the
# cone's targets, and its calls to track the cone. BREAK=1 passes targets
# without the cone's dependents and must print `caught` for the missed
# update. Until then this scenario fails before it touches anything.

command -v go >/dev/null 2>&1 || fail "cone" "this scenario generates its terralith with tools/terralith-gen and needs Go on PATH"
WORK="$SMOKE_WORKROOT/cone"
mkdir -p "$WORK"
go build -o "$WORK/terralith-gen" ./tools/terralith-gen || fail "cone" "tools/terralith-gen did not build"

fail "cone" "plan-cost-tracks-the-change is not written yet (GitHub issue #1956); claim 47 stays unproven"
