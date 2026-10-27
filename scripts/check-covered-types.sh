#!/usr/bin/env bash
# scripts/check-covered-types.sh <plunge checkout> [ref]
#
# INTENTIUS/choudoufu#2271: choudoufu and plunge list the same GCP types
# through each type's own service (INTENTIUS/choudoufu#2264,
# INTENTIUS/plunge#15), and each repo's test checks its own lister against
# its own copy of the list. This diffs the two copies, so a type added on one
# side is caught before either side's own test can notice:
#
#   choudoufu  pkg/gcpsweep/testdata/covered-types.txt
#   plunge     pkg/backend/plunge/gcplist/testdata/covered-types.txt
#
# With ref, plunge's copy is read from that git ref of the checkout
# (git show) rather than from its working tree, e.g.
#   scripts/check-covered-types.sh ../plunge origin/integration/engine-ship
#
# The last line is the verdict: "covered-types: SAME (<n> types)" with exit
# status 0, or "covered-types: DIFFERENT" / "covered-types: ERROR ..." with a
# nonzero status. Read the verdict line, not only the status.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ours="$here/pkg/gcpsweep/testdata/covered-types.txt"
rel="pkg/backend/plunge/gcplist/testdata/covered-types.txt"

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
	echo "usage: scripts/check-covered-types.sh <plunge checkout> [ref]" >&2
	echo "covered-types: ERROR usage"
	exit 2
fi
plunge="$1"
ref="${2:-}"

if [ ! -f "$ours" ]; then
	echo "covered-types: ERROR choudoufu's copy $ours is missing"
	exit 2
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
theirs="$tmp/plunge-covered-types.txt"
if [ -n "$ref" ]; then
	if ! git -C "$plunge" show "$ref:$rel" >"$theirs" 2>"$tmp/err"; then
		echo "covered-types: ERROR cannot read $rel at $ref in $plunge: $(cat "$tmp/err")"
		exit 2
	fi
	where="$plunge@$ref:$rel"
else
	if [ ! -f "$plunge/$rel" ]; then
		echo "covered-types: ERROR plunge's copy $plunge/$rel is missing"
		exit 2
	fi
	cp "$plunge/$rel" "$theirs"
	where="$plunge/$rel"
fi

# Type lines only: the comment header may say where each copy lives.
types() { grep -v '^[[:space:]]*#' "$1" | grep -v '^[[:space:]]*$' || true; }
types "$ours" >"$tmp/ours"
types "$theirs" >"$tmp/theirs"

n="$(wc -l <"$tmp/ours" | tr -d ' ')"
if [ "$n" -eq 0 ]; then
	echo "covered-types: ERROR choudoufu's copy lists no types"
	exit 2
fi

status=0
diff -u --label "choudoufu pkg/gcpsweep/testdata/covered-types.txt" --label "plunge $where" "$tmp/ours" "$tmp/theirs" || status=$?
if [ "$status" -eq 0 ]; then
	echo "covered-types: SAME ($n types)"
	exit 0
fi
if [ "$status" -eq 1 ]; then
	echo "covered-types: DIFFERENT (lines starting - are only in choudoufu, + only in plunge; add the type to both lists in the same wave)"
	exit 1
fi
echo "covered-types: ERROR diff exited $status"
exit 2
