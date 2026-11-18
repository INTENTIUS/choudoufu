#!/usr/bin/env bash
# scripts/release-notes.sh: write the release's notes file for `chant release`
# (chant.config.ts, the forge target's notes). The file holds one "## choudoufu
# vX.Y.Z" heading; chant publishes what follows it, then appends GitHub's
# generated "What's Changed" list.
#
# Both versions belong in the notes: the fork release and the upstream OpenTofu
# it is built on. The base is the fork point's own version/VERSION, verbatim: a
# -dev tree is unreleased upstream main, not the release it precedes (#1778).
# Version and fork point both come from live/fork-surface.json
# (tools/forkdiff-gen), so neither is typed here; the tree's own
# version/VERSION must agree, or that artifact is stale.
#
# The release's own CHANGELOG section (#2309) follows, so the release page
# carries the full notes. A tag with no section stops the release rather than
# publishing without notes.
set -euo pipefail

: "${CHANT_RELEASE_LABEL:?set by chant release cut}"
tag="v${CHANT_RELEASE_LABEL#v}"
out="${1:-dist/release-notes.md}"

surface=live/fork-surface.json
upstream=$(jq -r '.base_opentofu_version // empty' "$surface")
fork=$(jq -r '.fork_point_short // empty' "$surface")
tree=$(tr -d '[:space:]' < version/VERSION)
if [ -z "$upstream" ] || [ -z "$fork" ]; then
  echo "$surface lacks base_opentofu_version or fork_point_short" >&2
  exit 1
fi
if [ "$upstream" != "$tree" ]; then
  echo "$surface says base $upstream but version/VERSION reads $tree; run go run ./tools/forkdiff-gen" >&2
  exit 1
fi

section=$(mktemp)
trap 'rm -f "$section"' EXIT
awk -v h="## choudoufu $tag " 'index($0, h) == 1 { f = 1; next } f && /^## / { exit } f' CHANGELOG.md > "$section"
if ! grep -q '[^[:space:]]' "$section"; then
  echo "CHANGELOG.md has no section for $tag (a heading starting '## choudoufu $tag ')" >&2
  exit 1
fi

mkdir -p "$(dirname "$out")"
{
  echo "## choudoufu $tag"
  echo
  echo "choudoufu $tag, built on OpenTofu $upstream (fork point [\`$fork\`](https://github.com/opentofu/opentofu/commit/$fork))."
  echo
  echo "Everything outside live resource markers and the declared provider overrides is stock OpenTofu $upstream. The overrides are listed below and in \`live/overrides.json\`; \`CHOUDOUFU_PROVIDER_OVERRIDES=off\` turns them all off. Binaries below are macOS, Linux and Windows, amd64 and arm64; macOS and Linux are \`.tar.gz\`, Windows is \`.zip\`; verify with \`SHA256SUMS\`."
  echo
  cat "$section"
} > "$out"
