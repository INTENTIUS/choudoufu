#!/usr/bin/env bash
# scripts/release-build.sh: build one platform's choudoufu binary for
# `chant release` (chant.config.ts, release.artifacts). Called once per
# platform with CHANT_RELEASE_OS, CHANT_RELEASE_ARCH, CHANT_RELEASE_EXT and
# CHANT_RELEASE_LABEL set by the cut; writes dist/<os>_<arch>/choudoufu[.exe].
set -euo pipefail

: "${CHANT_RELEASE_LABEL:?set by chant release cut}"
: "${CHANT_RELEASE_OS:?set by chant release cut}"
: "${CHANT_RELEASE_ARCH:?set by chant release cut}"

# The fork version is the tag, "v" and all: the label 0.35.0 and v0.35.0
# both give v0.35.0, as release.tag "v{version}" does.
tag="v${CHANT_RELEASE_LABEL#v}"
out="dist/${CHANT_RELEASE_OS}_${CHANT_RELEASE_ARCH}/choudoufu${CHANT_RELEASE_EXT:-}"
mkdir -p "$(dirname "$out")"

# MODULE-PATH-RENAME: the -X flags below embed the Go module path
# github.com/intentius/choudoufu; update them when the module is renamed.
GOOS="$CHANT_RELEASE_OS" GOARCH="$CHANT_RELEASE_ARCH" CGO_ENABLED=0 \
  go build -trimpath \
    -ldflags "-X 'github.com/intentius/choudoufu/version.Fork=${tag}' -X 'github.com/intentius/choudoufu/version.dev=no'" \
    -o "$out" ./cmd/choudoufu

# Only a binary the runner can execute can report its version.
if [ "$CHANT_RELEASE_OS/$CHANT_RELEASE_ARCH" = "$(go env GOHOSTOS)/$(go env GOHOSTARCH)" ]; then
  "./$out" version
  "./$out" version | grep -F "choudoufu ${tag} (based on OpenTofu"
fi
