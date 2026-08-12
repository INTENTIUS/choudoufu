#!/usr/bin/env bash
# rename.sh - the rename phase (issue #17), as one idempotent transformation.
#
# Run from anywhere inside a clean checkout:
#
#	bash tools/rename-phase/rename.sh
#
# What it does, in order (moves first, so the content rewrites see final
# locations):
#
#   1. git mv: stateless/ -> live/, internal/stateless -> internal/live,
#      website/docs/language/stateless-mode.mdx -> live-markers.mdx,
#      internal/backend/local/stateless.go -> live.go, and the
#      internal/command{,/views}/stateless_*.go files -> live_*.go.
#   2. Module path: github.com/opentofu/opentofu ->
#      github.com/intentius/choudoufu in go.mod (module line and the tool
#      directives), every non-generated .go file, and the build plumbing
#      that bakes the path into ldflags or tool invocations (.tfdev,
#      .goreleaser.yaml, scripts/build.sh, scripts/debug-opentofu,
#      Makefile). Lines citing https://github.com/opentofu/opentofu
#      (upstream issues, PRs, labels) are left alone, as are the *.pb.go
#      descriptor blobs and ghcr.io/opentofu/opentofu image names.
#   3. Path references: internal/stateless -> internal/live and
#      stateless/{FAQ,LIMITATIONS,MARKERS,RECEIPTS,SURVEY}.md,
#      stateless/e2e -> the live/ spellings, across tracked files
#      (docs, docsRef strings, run.sh, site/main.go, ci.yml, justfile,
#      Makefile, templates). Runtime data like the "stateless-e2e" estate
#      name and tofu-* tag keys never match these patterns and stay put.
#   4. stateless-mode.mdx -> live-markers.mdx references, plus the
#      website nav entry "language/stateless-mode" -> "language/live-markers".
#      The stateless-mode.html redirect stub in site/main.go is deliberately
#      not touched.
#   5. Env vars STATELESS_E2E_* -> LIVE_E2E_*.
#   6. Filename citations: "stateless.go" / "stateless_*.go" in comments ->
#      the live_* spellings.
#   7. The root package of internal/live: package stateless -> package live
#      (no importer uses the qualifier, so this is two package clauses), and
#      coupling_test.go's "../../stateless" docs-dir constant.
#   8. README: the release badge becomes the pkg.go.dev badge; the FAQ's
#      download answer gains the go install command.
#   9. gofmt -w over anything gofmt -l flags among the tracked .go files.
#      This is not optional polish: gofmt sorts import blocks, and the
#      opentofu -> intentius rewrite changes where the module's own imports
#      sort relative to their neighbours, so dozens of files across the
#      whole tree come out of step 2 with unsorted imports.
#
# Every rewrite is guarded, so a rerun on already-converted trees is a no-op.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
[ -f go.mod ] || { echo "rename.sh: no go.mod at repo root" >&2; exit 1; }

note() { printf '==> %s\n' "$*"; }

# ---------------------------------------------------------------------------
# 1. moves
# ---------------------------------------------------------------------------

mv_tracked() {
	if [ -e "$1" ]; then
		note "git mv $1 $2"
		git mv "$1" "$2"
	fi
}

mv_tracked stateless live
mv_tracked internal/stateless internal/live
mv_tracked website/docs/language/stateless-mode.mdx website/docs/language/live-markers.mdx
mv_tracked internal/backend/local/stateless.go internal/backend/local/live.go
for f in internal/command/stateless_*.go internal/command/views/stateless_*.go; do
	[ -e "$f" ] || continue
	mv_tracked "$f" "${f/stateless_/live_}"
done

# ---------------------------------------------------------------------------
# rewrite machinery
# ---------------------------------------------------------------------------

# rewrite <label> <grep -E guard> <perl -p expr> <pathspec>...
# Applies the perl expression, line by line, to every tracked file under the
# pathspecs whose content matches the guard. Files that do not match are not
# rewritten, which is what makes reruns no-ops.
rewrite() {
	local label=$1 guard=$2 expr=$3 n=0 f
	shift 3
	while IFS= read -r -d '' f; do
		[ -f "$f" ] || continue
		if grep -qIE -- "$guard" "$f"; then
			perl -pi -e "$expr" "$f"
			n=$((n + 1))
		fi
	done < <(git ls-files -z -- "$@")
	note "$label: rewrote $n file(s)"
}

# Same, but slurps the whole file so the expression can span lines.
rewrite_slurp() {
	local label=$1 guard=$2 expr=$3 n=0 f
	shift 3
	while IFS= read -r -d '' f; do
		[ -f "$f" ] || continue
		if grep -qIE -- "$guard" "$f"; then
			perl -0777 -pi -e "$expr" "$f"
			n=$((n + 1))
		fi
	done < <(git ls-files -z -- "$@")
	note "$label: rewrote $n file(s)"
}

# Everything tracked except .claude worktrees, the website tree (upstream
# docs; the one renamed mdx is added back explicitly), generated protobuf
# code, go.sum, and this script's own directory.
MAIN_SPEC=(
	.
	':(exclude).claude'
	':(exclude)website'
	':(exclude)tools/rename-phase'
	':(exclude)*.pb.go'
	':(exclude)go.sum'
)
MDX=website/docs/language/live-markers.mdx
NAVJSON=website/data/language-nav-data.json

GO_SPEC=(
	'*.go'
	':(exclude).claude'
	':(exclude)website'
	':(exclude)tools/rename-phase'
	':(exclude)*.pb.go'
)

# ---------------------------------------------------------------------------
# 2. module path
# ---------------------------------------------------------------------------

# Replace the module path everywhere on a line except inside
# https://github.com/opentofu/opentofu URLs, which point at upstream and must
# keep doing so. \x01 is a scratch byte that never occurs in the tree.
MODULE_EXPR='s{https://github\.com/opentofu/opentofu}{\x01}g; s{github\.com/opentofu/opentofu}{github.com/intentius/choudoufu}g; s{\x01}{https://github.com/opentofu/opentofu}g'
# Guard: an occurrence not preceded by "/" (i.e. not the https:// form).
MODULE_GUARD='(^|[^/])github\.com/opentofu/opentofu'

rewrite "module path: go.mod" "$MODULE_GUARD" "$MODULE_EXPR" go.mod
rewrite "module path: .go files" "$MODULE_GUARD" "$MODULE_EXPR" "${GO_SPEC[@]}"
rewrite "module path: build plumbing" "$MODULE_GUARD" "$MODULE_EXPR" \
	.tfdev .goreleaser.yaml scripts/build.sh scripts/debug-opentofu Makefile

# ---------------------------------------------------------------------------
# 3. path references
# ---------------------------------------------------------------------------

# NOTE: the ':(exclude)website' pathspec in MAIN_SPEC applies to the whole
# git ls-files invocation, positive pathspecs included, so the renamed mdx
# cannot ride along in the same call: it gets its own.
rewrite "paths: internal/stateless -> internal/live" \
	'internal/stateless' \
	's{internal/stateless}{internal/live}g' \
	"${MAIN_SPEC[@]}"
rewrite "paths: internal/stateless (concept page)" \
	'internal/stateless' \
	's{internal/stateless}{internal/live}g' \
	"$MDX"

DOCPATH_GUARD='stateless/(FAQ|LIMITATIONS|MARKERS|RECEIPTS|SURVEY)\.md|stateless/e2e'
DOCPATH_EXPR='s{stateless/(FAQ|LIMITATIONS|MARKERS|RECEIPTS|SURVEY)\.md}{live/$1.md}g; s{stateless/e2e}{live/e2e}g'
rewrite "paths: stateless/ docs and e2e -> live/" \
	"$DOCPATH_GUARD" "$DOCPATH_EXPR" "${MAIN_SPEC[@]}"
rewrite "paths: stateless/ docs and e2e (concept page)" \
	"$DOCPATH_GUARD" "$DOCPATH_EXPR" "$MDX"

# One prose mention of the docs directory itself, backtick-quoted, in the
# concept page. The estate names ("stateless-e2e-block") on the same page
# are runtime data and deliberately survive.
rewrite "paths: bare \`stateless/\` directory mention" \
	'`stateless/`' \
	's{`stateless/`}{`live/`}g' \
	"${MAIN_SPEC[@]}"
rewrite "paths: bare \`stateless/\` directory mention (concept page)" \
	'`stateless/`' \
	's{`stateless/`}{`live/`}g' \
	"$MDX"

# Tests reach the live/ fixtures via filepath.Join component lists
# ("stateless", "e2e", ...), which the slash-joined patterns above cannot
# see. The trailing quote keeps this away from runtime data such as
# projection/manager.go's `return "stateless", nil` lock id.
rewrite 'paths: "stateless" filepath.Join components' \
	'"stateless", "' \
	's{"stateless", "}{"live", "}g' \
	"${GO_SPEC[@]}"

# coupling_test.go points at the docs dir as a bare relative path.
rewrite "paths: coupling_test.go docs-dir constant" \
	'\.\./\.\./stateless"' \
	's{\.\./\.\./stateless"}{../../live"}g' \
	internal/live/coupling_test.go

# ---------------------------------------------------------------------------
# 4. the concept page
# ---------------------------------------------------------------------------

rewrite "concept page: stateless-mode.mdx -> live-markers.mdx" \
	'stateless-mode\.mdx' \
	's{stateless-mode\.mdx}{live-markers.mdx}g' \
	"${MAIN_SPEC[@]}"
rewrite "concept page: self-references" \
	'stateless-mode\.mdx' \
	's{stateless-mode\.mdx}{live-markers.mdx}g' \
	"$MDX"

rewrite "concept page: website nav entry" \
	'"language/stateless-mode"' \
	's{"language/stateless-mode"}{"language/live-markers"}g' \
	"$NAVJSON"

# ---------------------------------------------------------------------------
# 5. env vars
# ---------------------------------------------------------------------------

rewrite "env vars: STATELESS_E2E_ -> LIVE_E2E_" \
	'STATELESS_E2E_' \
	's{STATELESS_E2E_}{LIVE_E2E_}g' \
	"${MAIN_SPEC[@]}"

# ---------------------------------------------------------------------------
# 6. filename citations
# ---------------------------------------------------------------------------

rewrite "filenames: stateless*.go citations" \
	'\bstateless(_[a-z0-9_]+)?\.go\b' \
	's{\bstateless((?:_[a-z0-9_]+)?\.go)\b}{live$1}g' \
	"${MAIN_SPEC[@]}"
rewrite "filenames: stateless*.go citations (concept page)" \
	'\bstateless(_[a-z0-9_]+)?\.go\b' \
	's{\bstateless((?:_[a-z0-9_]+)?\.go)\b}{live$1}g' \
	"$MDX"

# ---------------------------------------------------------------------------
# 7. the root package of internal/live
# ---------------------------------------------------------------------------

rewrite "package clause: internal/live" \
	'^package stateless(_test)?$' \
	's{^package stateless(_test)?$}{package live$1}' \
	internal/live/doc.go internal/live/coupling_test.go

# ---------------------------------------------------------------------------
# 8. README badge and FAQ download answer
# ---------------------------------------------------------------------------

rewrite_slurp "README: pkg.go.dev badge" \
	'img\.shields\.io/github/v/release/INTENTIUS/choudoufu' \
	's{\[!\[Release\]\(https://img\.shields\.io/github/v/release/INTENTIUS/choudoufu\)\]\(https://github\.com/INTENTIUS/choudoufu/releases\)}{[![Go Reference](https://pkg.go.dev/badge/github.com/intentius/choudoufu.svg)](https://pkg.go.dev/github.com/intentius/choudoufu)}' \
	README.md

if ! grep -q 'go install' live/FAQ.md; then
	perl -0777 -pi -e 's{with checksums\. Building}{with checksums. Or skip the download and let Go\nbuild it: `go install github.com/intentius/choudoufu/cmd/choudoufu\@latest`.\nBuilding}' live/FAQ.md
	note "FAQ: added the go install answer"
else
	note "FAQ: go install answer already present"
fi

# ---------------------------------------------------------------------------
# 9. gofmt
# ---------------------------------------------------------------------------

UNFORMATTED="$(git ls-files -z -- "${GO_SPEC[@]}" | xargs -0 gofmt -l)"
if [ -n "$UNFORMATTED" ]; then
	n=0
	while IFS= read -r f; do
		gofmt -w "$f"
		n=$((n + 1))
	done <<<"$UNFORMATTED"
	note "gofmt -w: reformatted $n file(s) (import sort order moved)"
else
	note "gofmt: clean"
fi

note "done. suggested checks:"
note "  go build ./cmd/choudoufu"
note "  go test ./internal/live/... ./internal/configs/... ./internal/command/"
note "  (cd site && go run . -out /tmp/site-test)"
note "  bash -n live/e2e/run.sh"
note "  gofmt -l internal/live cmd site"
