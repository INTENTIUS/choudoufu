#!/usr/bin/env bash
# scripts/upstream-sync-test.sh: the red cases and the idempotency check for
# scripts/upstream-sync.sh (#2335). Written in U0, run in #2256's end phase.
#
# Usage, from a worktree whose HEAD carries the graft chain:
#
#   scripts/upstream-sync-test.sh
#
# It needs the `upstream` remote fetched (v1.13.2 and aa3eb66a0c present)
# and makes no network calls itself. Every case runs in a throwaway
# detached worktree on a throwaway branch, so the calling worktree, its
# branch and its index are never touched. The only lasting side effect is
# upstream-sync.sh's own sync/<short>-graft branch for the ref in (b-control).
#
# Each case prints one verdict line, "PASS (x) ..." or "FAIL (x) ...", and
# the run ends with "upstream-sync-test: N passed, M failed". Read those
# lines. A case passes only on the named error or the measured tree, never
# on an exit code alone.
#
# Cases:
#   (a1) --prev-graft at a commit that is not a graft: E_WRONG_PREV_GRAFT
#   (a2) --prev-graft at an older graft for a merge: E_WRONG_PREV_GRAFT
#   (a3) --prev-graft not on HEAD's ancestry: E_WRONG_PREV_GRAFT
#   (a4) a forged graft whose tree is not the upstream commit it claims,
#        found by auto-discovery: E_WRONG_PREV_GRAFT
#   (b)  rewrite skipped (UPSTREAM_SYNC_SKIP_REWRITE=1): E_REWRITE_INCOMPLETE,
#        before any merge is attempted
#   (b-control) the same run without the hook gets past the twin check
#   (c)  graft-only at v1.13.2 on 2958e54f45: the graft's tree equals
#        93bb2800b4's, and its parent is 2958e54f45
#   (d)  two runs at the ref the latest graft carries: HEAD unchanged and
#        nothing to commit after each

set -uo pipefail

top=$(git rev-parse --show-toplevel) || exit 1
sync=$top/scripts/upstream-sync.sh
work=$(mktemp -d "${TMPDIR:-/tmp}/upstream-sync-test.XXXXXX")
wt=$work/wt
branch=upstream-sync-test/$$
forged_branch=upstream-sync-test/$$-forged
pass=0
fail=0

cleanup() {
	git -C "$top" worktree remove --force "$wt" >/dev/null 2>&1 || true
	git -C "$top" branch -D "$branch" "$forged_branch" >/dev/null 2>&1 || true
	rm -rf "$work"
}
trap cleanup EXIT

verdict() { # verdict ok|bad case text
	if [ "$1" = ok ]; then
		echo "PASS ($2) $3"
		pass=$((pass + 1))
	else
		echo "FAIL ($2) $3"
		fail=$((fail + 1))
	fi
}

# run_sync OUTFILE ARGS...: runs upstream-sync.sh in the throwaway worktree.
run_sync() {
	local out=$1
	shift
	(cd "$wt" && "$sync" "$@") >"$out" 2>&1
}

# expect_error CASE CODE DESC ARGS...: the run must print the named error.
expect_error() {
	local c=$1 code=$2 desc=$3 out
	shift 3
	out=$work/$c.out
	run_sync "$out" "$@"
	if grep -q "upstream-sync: error $code:" "$out"; then
		verdict ok "$c" "$desc: $(grep -m1 "error $code:" "$out")"
	else
		verdict bad "$c" "$desc: expected error $code, got: $(tail -n 3 "$out" | tr '\n' ' ')"
	fi
}

git -C "$top" worktree add -q --detach "$wt" HEAD || {
	echo "FAIL (setup) could not create a throwaway worktree"
	exit 1
}
git -C "$wt" switch -q -c "$branch" || {
	echo "FAIL (setup) could not create branch $branch"
	exit 1
}
base=$(git -C "$wt" rev-parse HEAD)

latest=$(git -C "$wt" log --format='%H%x09%s' HEAD | awk -F '\t' '$2 ~ /^(graft|upstream): OpenTofu / { print $1; exit }')
if [ -z "$latest" ]; then
	echo "FAIL (setup) no graft commit on HEAD's history"
	exit 1
fi
latest_claim=$(git -C "$wt" log -1 --format='%(trailers:key=Upstream-Commit,valueonly)' "$latest" | head -n1 | tr -d '[:space:]')
if [ -z "$latest_claim" ]; then
	latest_claim=$(git -C "$wt" log -1 --format=%s "$latest" | grep -oE '\([0-9a-f]{7,40}\)' | head -n1 | tr -d '()')
fi
latest_claim=$(git -C "$wt" rev-parse "$latest_claim^{commit}")
# A ref whose tree differs from the latest graft's, so a run reaches step 2.
if [ "$(git -C "$wt" rev-parse "$latest_claim^{tree}")" = "$(git -C "$wt" rev-parse 'aa3eb66a0c^{tree}')" ]; then
	other_ref=v1.13.2
else
	other_ref=aa3eb66a0c
fi
echo "upstream-sync-test: HEAD $(git -C "$wt" rev-parse --short=10 HEAD), latest graft $(git -C "$wt" rev-parse --short=10 "$latest") carrying $(git -C "$wt" rev-parse --short=10 "$latest_claim"); other ref $other_ref"

# (a) wrong previous graft
expect_error a1 E_WRONG_PREV_GRAFT "prev graft is HEAD, not a graft" \
	--graft-only --no-fetch --prev-graft HEAD v1.13.2
if [ "$latest" != "$(git -C "$wt" rev-parse 2958e54f45)" ]; then
	expect_error a2 E_WRONG_PREV_GRAFT "prev graft 2958e54f45 is older than the latest graft" \
		--no-fetch --no-report --prev-graft 2958e54f45 "$other_ref"
else
	verdict bad a2 "2958e54f45 is the latest graft on HEAD; this case needs a newer one"
fi
expect_error a3 E_WRONG_PREV_GRAFT "prev graft upstream/main is not on HEAD's ancestry" \
	--graft-only --no-fetch --prev-graft upstream/main v1.13.2

forged=$(git -C "$wt" commit-tree "HEAD^{tree}" -p HEAD -m "graft: OpenTofu v1.13.2 tree (765062032c) on graft 2958e54f45 (forged by upstream-sync-test)")
git -C "$wt" switch -q -c "$forged_branch" "$forged"
expect_error a4 E_WRONG_PREV_GRAFT "auto-discovered graft whose tree is not the upstream commit it claims" \
	--no-fetch --no-report "$other_ref"
if grep -q "tree differs" "$work/a4.out"; then
	verdict ok a4-reason "a4 failed on the tree check, not another rule"
else
	verdict bad a4-reason "a4 did not fail on the tree check: $(grep -m1 error "$work/a4.out")"
fi
git -C "$wt" switch -q "$branch"

# (b) skipped rewrite
out=$work/b.out
(cd "$wt" && UPSTREAM_SYNC_SKIP_REWRITE=1 "$sync" --no-fetch --no-report "$other_ref") >"$out" 2>&1
if grep -q "upstream-sync: error E_REWRITE_INCOMPLETE:" "$out" && ! grep -q "^CONFLICTS:" "$out"; then
	verdict ok b "skipped rewrite: $(grep -m1 'error E_REWRITE_INCOMPLETE:' "$out")"
else
	verdict bad b "skipped rewrite was not caught before the merge: $(tail -n 3 "$out" | tr '\n' ' ')"
fi
out=$work/b-control.out
run_sync "$out" --no-fetch --no-report "$other_ref"
if grep -q "E_REWRITE_INCOMPLETE" "$out"; then
	verdict bad b-control "the real rewrite failed the twin check: $(grep -m1 E_REWRITE_INCOMPLETE "$out")"
elif grep -qE "^CONFLICTS:|^upstream-sync: merged:" "$out"; then
	verdict ok b-control "real rewrite passed the twin check and reached the merge: $(grep -m1 -E '^CONFLICTS:|merged:' "$out")"
else
	verdict bad b-control "run did not reach the merge: $(tail -n 3 "$out" | tr '\n' ' ')"
fi
# A clean merge in b-control would have committed; put the branch back.
git -C "$wt" reset -q --hard "$base"

# (c) graft reproduces 93bb2800b4's tree
out=$work/c.out
run_sync "$out" --graft-only --no-fetch --prev-graft 2958e54f45 v1.13.2
g=$(tail -n 1 "$out")
if git -C "$wt" rev-parse -q --verify "$g^{commit}" >/dev/null 2>&1; then
	gt=$(git -C "$wt" rev-parse "$g^{tree}")
	want=$(git -C "$wt" rev-parse '93bb2800b4^{tree}')
	gp=$(git -C "$wt" rev-parse "$g^@")
	wantp=$(git -C "$wt" rev-parse 2958e54f45)
	if [ "$gt" = "$want" ] && [ "$gp" = "$wantp" ]; then
		verdict ok c "graft $g has tree $gt (= 93bb2800b4's) and sole parent 2958e54f45"
	else
		verdict bad c "graft $g tree $gt parents [$gp]; want tree $want, parent $wantp"
	fi
else
	verdict bad c "graft-only printed no commit: $(tail -n 3 "$out" | tr '\n' ' ')"
fi

# (d) idempotent at the ref already merged
before=$(git -C "$wt" rev-parse HEAD)
for i in 1 2; do
	out=$work/d$i.out
	run_sync "$out" --no-fetch --no-report "$latest_claim"
	after=$(git -C "$wt" rev-parse HEAD)
	st=$(git -C "$wt" status --porcelain)
	if grep -q "already merged" "$out" && ! grep -q "upstream-sync: error" "$out" && [ "$after" = "$before" ] && [ -z "$st" ]; then
		verdict ok "d$i" "run $i at $(git -C "$wt" rev-parse --short=10 "$latest_claim"): HEAD unchanged, nothing to commit"
	else
		verdict bad "d$i" "run $i: HEAD $before -> $after, status [$st], output: $(tail -n 3 "$out" | tr '\n' ' ')"
	fi
done

echo "upstream-sync-test: $pass passed, $fail failed"
[ "$fail" = 0 ]
