#!/usr/bin/env bash
# scripts/cut-release.sh: cut a choudoufu release locally, the procedure in
# CHANGELOG.md's "Cutting a release", in two commands.
#
#   scripts/cut-release.sh cut vX.Y.Z   release branch in its own worktree,
#                                       board snapshot, CHANGELOG dated, commit,
#                                       push, pull request
#   scripts/cut-release.sh tag vX.Y.Z   once that pull request has merged: tag
#                                       the merge commit and push the tag, which
#                                       starts .github/workflows/release.yml
#
# No gauntlet runs. The board snapshot is a copy of live/gauntlet.json as
# main has it, which is what `gauntlet snapshot` writes too, and the board
# movement is read from the two snapshot files. Whatever the board says on
# main is what the release records.
#
# The release pull request's GitHub CI is its gate. After the tag, the
# examples/ci-pipelines pin is still the follow-up pull request CHANGELOG.md's
# step 6 describes.
set -euo pipefail

usage() { echo "usage: $0 cut|tag vX.Y.Z" >&2; exit 2; }
die() { echo "cut-release: $*" >&2; exit 1; }

[ $# -eq 2 ] || usage
CMD="$1" V="$2"
[[ "$V" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "version must look like v0.25.0, got '$V'"
ROOT="$(git rev-parse --show-toplevel)"
BRANCH="release/$V"

cut() {
  git -C "$ROOT" fetch -q origin main --tags
  git -C "$ROOT" rev-parse -q --verify "refs/tags/$V" >/dev/null && die "tag $V already exists"
  [ -e "$ROOT/live/history/$V.json" ] && die "live/history/$V.json already exists on this checkout"

  local wt="$ROOT/../wt/release-$V"
  [ -e "$wt" ] && die "$wt already exists; remove it or finish that release"
  git -C "$ROOT" worktree add -q -b "$BRANCH" "$wt" origin/main
  cd "$wt"

  local prev
  prev="$(ls live/history | sed -n 's/^\(v[0-9]*\.[0-9]*\.[0-9]*\)\.json$/\1/p' | sort -V | tail -1)"
  [ -n "$prev" ] || die "no previous release in live/history"

  cp live/gauntlet.json "live/history/$V.json"
  local movement
  movement="$(env -u PWD go run ./tools/gauntlet notes "live/history/$prev.json" "live/history/$V.json")" \
    || die "could not read the board movement between $prev and $V"
  # notes prints "## " section headings; under a "## choudoufu vX.Y.Z" heading
  # they belong one level down.
  movement="$(sed 's/^## /### /' <<< "$movement")"

  local base today
  base="$(tr -d '[:space:]' < version/VERSION)"
  today="$(date +%Y-%m-%d)"
  python3 - "$V" "$prev" "$base" "$today" "$movement" <<'PY' || exit 1
import re, sys
v, prev, base, today, movement = sys.argv[1:]
p = "CHANGELOG.md"
s = open(p).read()
head = "## choudoufu %s (Unreleased)" % v
if s.count(head) != 1:
    sys.exit("cut-release: CHANGELOG.md has no '%s' heading; the open (Unreleased) heading must name the version being cut" % head)
major, minor, _ = (int(x) for x in v[1:].split("."))
nxt = "v%d.%d.0" % (major, minor + 1)
body = (
    "## choudoufu %s (Unreleased)\n\n"
    "## choudoufu %s (%s)\n\n"
    "Built on OpenTofu %s. Board snapshot: [`live/history/%s.json`](live/history/%s.json).\n\n"
    "BOARD MOVEMENT (from `go run ./tools/gauntlet notes live/history/%s.json live/history/%s.json`):\n\n"
    "%s\n"
) % (nxt, v, today, base, v, v, prev, v, movement.strip())
open(p, "w").write(s.replace(head + "\n", body, 1))
PY

  git add CHANGELOG.md "live/history/$V.json"
  git commit -q -m "release: cut $V

Built on OpenTofu $base. Board snapshot live/history/$V.json, movement against $prev:

$movement"
  git push -q -u origin "$BRANCH"
  gh pr create --base main --head "$BRANCH" --title "release: cut $V" --body "Cuts $V with \`scripts/cut-release.sh cut $V\`: \`live/history/$V.json\` copied from main's \`live/gauntlet.json\` (no gauntlet run), and CHANGELOG dated $today with a fresh empty (Unreleased) heading above it.

Board movement against $prev:

$movement

After merge: \`scripts/cut-release.sh tag $V\`, then the examples/ci-pipelines pin pull request (CHANGELOG.md, step 6)."
  echo "cut-release: $V is in a pull request from $BRANCH (worktree $wt). When it merges: $0 tag $V"
}

tag() {
  git -C "$ROOT" fetch -q origin main --tags
  git -C "$ROOT" rev-parse -q --verify "refs/tags/$V" >/dev/null && die "tag $V already exists"
  local state merge
  state="$(gh pr view "$BRANCH" --json state -q .state)" || die "no pull request from $BRANCH"
  [ "$state" = "MERGED" ] || die "the pull request from $BRANCH is $state, not MERGED"
  merge="$(gh pr view "$BRANCH" --json mergeCommit -q .mergeCommit.oid)"
  [ -n "$merge" ] || die "the merged pull request names no merge commit"
  git -C "$ROOT" merge-base --is-ancestor "$merge" origin/main || die "merge commit $merge is not on origin/main"
  git -C "$ROOT" tag -a "$V" -m "choudoufu $V" "$merge"
  git -C "$ROOT" push -q origin "refs/tags/$V"
  echo "cut-release: tagged $merge as $V and pushed it; release.yml builds and publishes the release"
}

case "$CMD" in
  cut) cut ;;
  tag) tag ;;
  *) usage ;;
esac
