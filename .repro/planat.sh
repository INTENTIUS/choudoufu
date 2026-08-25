#!/usr/bin/env bash
# build choudoufu at $1 in the bisect worktree, plan the already-applied
# greenfield workdir, print rc and whether the wrong-marker refusal fires.
set -uo pipefail
SHA="$1"
BW="/Users/alex/Documents/checkouts/intentius/wt/wm-bisect"
WORK="/Users/alex/Documents/checkouts/intentius/wt/wrong-marker/.repro/work"
BIN="/Users/alex/Documents/checkouts/intentius/wt/wrong-marker/.repro/bin/$SHA"
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1 AWS_ENDPOINT_URL="http://127.0.0.1:${FLOCI_PORT:-5420}"
git -C "$BW" checkout -q --detach "$SHA" 2>/dev/null || { echo "$SHA checkout FAILED"; exit 2; }
mkdir -p "$(dirname "$BIN")"
( cd "$BW" && env -u PWD go build -o "$BIN" ./cmd/choudoufu ) > "$WORK/build-$SHA.log" 2>&1 || { echo "$SHA BUILD-FAILED"; tail -5 "$WORK/build-$SHA.log"; exit 2; }
( cd "$WORK" && "$BIN" plan -input=false -no-color ) > "$WORK/plan-$SHA.log" 2>&1; RC=$?
if grep -q "Malformed ownership marker" "$WORK/plan-$SHA.log"; then M=MALFORMED; else M=no-malformed; fi
if grep -qF "No changes." "$WORK/plan-$SHA.log"; then E=empty; else E=NOT-EMPTY; fi
echo "$SHA rc=$RC $M $E"
