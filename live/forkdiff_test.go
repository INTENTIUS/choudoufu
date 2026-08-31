// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"strings"
	"testing"
)

// This file is issue #423's guard on live/fork-surface.json
// (tools/forkdiff-gen), the measurement of "everything outside live
// resource markers is stock OpenTofu": every path that differs between HEAD
// and the fork point, grouped by top-level root.
//
// Five of the six named roots (internal/live/, tools/, live/, site/, rulings/)
// are entirely fork-authored trees, and the sixth (.github/) is nearly all
// new workflows with one edited stock one - none of that needs policing.
// What needs policing is the "other" bucket: every path outside those
// roots, which is where a hand-edit to a stock-owned package would show up.
// otherAllowlist is the one place that is allowed to be non-empty, and only
// with a reason attached to each entry - the same shape as
// ci_coverage_test.go's ciExcludedPackages and flociimage_test.go's
// staleFlociMeasurements next door: a standing decision the tree is checked
// against, not a hand list nothing verifies.
//
// Entries are path prefixes, not exact files, because the real other bucket
// is dominated by whole fork-owned subtrees sitting inside stock-owned Go
// packages (internal/command's live_*.go files, internal/configs' live.go
// and static evaluator, and so on) - the same shape
// ci_coverage_test.go's forkOwnedMixedRoots already names for
// internal/command, internal/engine/applying and internal/tofu. A prefix
// entry covers a whole such subtree with one reason instead of one reason
// per file, while still failing the moment something lands outside every
// covered prefix.

// otherAllowEntry is one accounted-for slice of the other bucket.
type otherAllowEntry struct {
	// Prefix is matched against a fork-surface.json path with
	// strings.HasPrefix; an exact root-level filename (no trailing slash)
	// matches only itself.
	Prefix string
	Reason string
}

// otherAllowlist is issue #423's allowlist. Every entry was written after
// reading the actual diff it covers (see tools/forkdiff-gen's own PR), not
// guessed from the path alone.
var otherAllowlist = []otherAllowEntry{
	// internal/, package by package. Each of these packages is upstream's,
	// and each holds either whole fork-authored files, a small wiring edit
	// to a stock file, or both.
	{"internal/command/", "the fork's command surface: live_* files (live_plan, live_import, live_mv, live_check, discovery/lint guards, their views) plus wiring edits to stock commands (init, apply, plan, state*, meta_backend, workspace*, providers*) that call into it; mirrors ci_coverage_test.go's forkOwnedMixedRoots entry for this package"},
	{"internal/configs/", "the live block, record_store and strict{} config schema and HCL parsing (live.go, parser_live_sidecar.go), plus the static evaluator and static scope (rulings/20260823-foundation-order-ruling.md item 3) that live-import, live-mv, live-check and discovery consume"},
	{"internal/tofu/", "the plan-node seam: identity resolution and marker stamping hooked into node_resource_plan_instance.go and resource_identity.go, plus the graph-walk and evaluation plumbing they need; mirrors forkOwnedMixedRoots"},
	{"internal/engine/", "internal/engine/applying/operations_resource_managed.go keeps the create-time provisioner's `self` value's sensitivity marks (forkOwnedMixedRoots, issue #353's follow-up audit)"},
	{"internal/backend/", "the local backend (and its s3 backend test fixtures) wires the live record store into init/plan/apply, and renames the `tofu init` suggestion text to `choudoufu init`"},
	{"internal/cloud/", "the same command-name substitution (`tofu login` -> `choudoufu login`) in the cloud backend's user-facing text"},
	{"internal/plugin/", "grpc_provider.go/grpc_provider_list.go carry the provider's list-resource-type schema (ListResourceTypes) through to the client the live layer's discovery reads"},
	{"internal/plugin6/", "the plugin protocol v6 counterpart of internal/plugin/ above"},
	{"internal/plugins/", "provider.go/provisioner.go: the module-path rename plus the same ListResourceTypes plumbing as internal/plugin/"},
	{"internal/lang/", "eval.go's EvalContextTolerant: one refusing reference no longer takes down every other, answerable reference in the same batch, so a config with nine static references and one dynamic one can still resolve the nine - the live static evaluator's use case"},
	{"internal/resources/", "managed_plan.go shares the RequiresReplacePathIsDegenerate handling internal/plans/ adds below with internal/tofu's marker-safe replace logic"},
	{"internal/plans/", "requires_replace_path.go (new file): RequiresReplacePathIsDegenerate, the empty-attribute-name check a provider's RequiresReplace path can carry"},
	{"internal/providers/", "the Provider interface grew ListResourceTypes, the schema the plugin clients above populate"},
	{"internal/provider-simple/", "the module-path rename plus the same ListResourceTypes plumbing, in the in-process test provider double"},
	{"internal/provider-simple-v6/", "the protocol v6 counterpart of internal/provider-simple/ above"},
	{"internal/tfdiags/", "hcl.go: a diagnostic's ExtraInfo now survives the round trip through configs' static evaluator (issue #178) instead of being silently dropped"},
	{"internal/builtin/", "internal/builtin/providers/tf/provider.go: the module-path rename plus a blank-line cleanup; no logic change"},
	{"internal/e2e/", "e2e.go: the module-path rename, including a `-coverpkg=` build-flag string value the quoted-import filter does not reach because it is not itself a quoted import line"},
	{"internal/getproviders/", "package_authentication.go: the module-path rename plus a doc comment repointed at upstream's own copy of the provider-registry-hashes design document, after this fork retired its rfc/ directory"},

	// cmd/: the binary entry point moved from cmd/tofu to cmd/choudoufu.
	// --no-renames (see tools/forkdiff-gen's diffNameStatus doc) reports
	// that as a delete plus an add rather than a move.
	{"cmd/tofu/", "deleted: the fork's binary entry point moved to cmd/choudoufu (next entry)"},
	{"cmd/choudoufu/", "the fork's binary entry point, renamed from cmd/tofu: registers the `live` subcommand family in the command table and reports the Fork version banner (version/version.go below)"},

	// Fork documentation, tooling and process files with no code content.
	{"docs/", "product docs and imagery rewritten or added for choudoufu (docs/README.md, docs/architecture.md, docs/images, docs/diagrams)"},
	{"rfc/", "deleted: upstream's RFC directory, retired wholesale because this fork does not run a request-for-comment process. The 11 fork-authored records moved to rulings/, a named root above; everything else here was upstream's, including two documents dated 2026, and stays in git history"},
	{"scripts/", "build.sh and debug-opentofu carry the module-path and binary-name rename; contribute.sh, pickup.sh and render-logo.sh are new fork tooling (pickup.sh is HANDOFF.md's required first command)"},
	{".claude/", "agent briefs and scripts for the gauntlet workflow (gauntlet-orchestrator.md, gauntlet-worker.md, live-markers.md, agent-progress.sh) plus the measuring-choudoufu skill"},
	{"contributing/", "DEVELOPING.md and FAQ.md updated for the fork; LIVE-TABLES.md and contributing/README.md are new"},
	{"website/package-lock.json", "a dependency lockfile refresh alongside the docs/site rework; no fork logic"},
	{"website/README.md", "a note marking the directory as inherited from upstream and unpublished by this fork, whose own site is site/; upstream's instructions below it are unchanged"},
	{"package.json", "new: the prose-lint dependency manifest; report-only Markdown linting for this fork's hand-written docs"},
	{"package-lock.json", "checksums for the package.json entry above"},
	{"prose-lint.mjs", "new: the prose linter itself, run over site/content/docs, live/ and rulings/"},
	{"version/version.go", "adds the Fork release-version var: the release tag choudoufu was built at, empty for dev builds"},

	// Root-level governance and identity documents: rewritten for choudoufu
	// (name, links, process), content rather than code, and not worth a
	// separate reason per file.
	{"README.md", "root governance/identity doc rewritten for choudoufu; content, not code"},
	{"AGENTS.md", "root governance/identity doc rewritten for choudoufu; content, not code"},
	{"CHANGELOG.md", "root governance/identity doc rewritten for choudoufu; content, not code"},
	{"CHARTER.md", "root governance/identity doc rewritten for choudoufu; content, not code"},
	{"CODE_OF_CONDUCT.md", "root governance/identity doc rewritten for choudoufu; content, not code"},
	{"CONTRIBUTING.md", "root governance/identity doc rewritten for choudoufu; content, not code"},
	{"GOVERNANCE.md", "root governance/identity doc rewritten for choudoufu; content, not code"},
	{"MAINTAINERS.md", "root governance/identity doc rewritten for choudoufu; content, not code"},
	{"SECURITY.md", "root governance/identity doc rewritten for choudoufu; content, not code"},
	{"CODEOWNERS", "deleted: choudoufu does not carry GitHub's per-path review-routing file; HANDOFF.md and the gauntlet briefs are this fork's routing instead"},
	{"HANDOFF.md", "new: this fork's single entry point, described in its own header"},
	{"BUG_REPORTS.md", "root governance/identity doc rewritten for choudoufu; content, not code"},
	{"CLAUDE.md", "new: the repository-wide rules every agent session loads, alongside HANDOFF.md; content, not code"},
	{"justfile", "new: `just <recipe>` is how every generator, `just ci` and `just contribute` are run in this fork"},
	{"Makefile", "build targets renamed or added for the fork (the choudoufu binary, floci/gauntlet targets); stock's own targets are unchanged where they still apply"},
	{".goreleaser.yaml", "release artifact naming (choudoufu, not tofu) and the Fork version ldflag from version/version.go"},
	{".gitignore", "fork-specific build/output and .claude/ scratch paths added"},
	{".gitmodules", "new: site/themes/hugo-book, the docs site's Hugo theme submodule"},
	{".tfdev", "the dev-mode version_var/prerelease_var paths, renamed the same way scripts/build.sh's LD_FLAGS is"},
	{".sc.png", "new: an image asset the fork's own docs reference"},
	{"go.mod", "the module path rename (github.com/opentofu/opentofu -> github.com/intentius/choudoufu) plus the dependencies live/ code needs"},
	{"go.sum", "checksums for the go.mod entry above"},

	// A housekeeping finding, not a stock edit: flagged here rather than
	// silently allowlisted away, and left for a separate cleanup - out of
	// scope for issue #423.
	{"tmp/admission-pipeline/", "a generator report that landed in a scratch directory and was committed; a housekeeping cleanup to do separately, not a stock edit"},
}

// forkSurfaceFile mirrors tools/forkdiff-gen's fileChange.
type forkSurfaceFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// forkSurfaceDoc decodes only the fields this guard needs from
// live/fork-surface.json.
type forkSurfaceDoc struct {
	ForkPoint string                       `json:"fork_point"`
	Counts    map[string]int               `json:"counts"`
	Files     map[string][]forkSurfaceFile `json:"files"`
}

// forkSurfaceForkPoint is the fixed commit tools/forkdiff-gen diffs HEAD
// against (see that package's doc comment for why it is a literal hash and
// not something re-derived from git ancestry). Pinned here too, so a future
// regeneration against the wrong commit - the fork point moving without a
// deliberate, documented decision - fails loudly instead of quietly
// widening or narrowing what every other check in this file assumes.
const forkSurfaceForkPoint = "03743ce6e8ddbc18c72a4fddb4d1a2ff8e765df5"

func loadForkSurface(t *testing.T) forkSurfaceDoc {
	t.Helper()
	var doc forkSurfaceDoc
	decodeInto(t, "fork-surface.json", &doc)
	return doc
}

// TestForkSurfaceForkPointIsPinned holds live/fork-surface.json's fork_point
// to the commit this fork actually diverged from.
func TestForkSurfaceForkPointIsPinned(t *testing.T) {
	doc := loadForkSurface(t)
	if doc.ForkPoint != forkSurfaceForkPoint {
		t.Errorf("live/fork-surface.json records fork_point %q; want %q.\n"+
			"The fork point only moves by a deliberate, documented decision (see tools/forkdiff-gen's package doc) - "+
			"if this is that decision, update forkSurfaceForkPoint here to match",
			doc.ForkPoint, forkSurfaceForkPoint)
	}
}

// TestForkSurfaceOtherBucketIsAllowlisted is this file's main guard: the
// other bucket must be empty, or every entry in it must be covered by
// otherAllowlist.
func TestForkSurfaceOtherBucketIsAllowlisted(t *testing.T) {
	doc := loadForkSurface(t)
	other, ok := doc.Files["other"]
	if !ok {
		t.Fatal(`live/fork-surface.json has no "other" key under files; regenerate it with go run ./tools/forkdiff-gen`)
	}

	matched := make([]bool, len(otherAllowlist))
	for _, f := range other {
		hit := -1
		for i, a := range otherAllowlist {
			if f.Path == a.Prefix || strings.HasPrefix(f.Path, a.Prefix) {
				hit = i
				break
			}
		}
		if hit == -1 {
			t.Errorf("live/fork-surface.json: %q (%s) is in the other bucket with no otherAllowlist entry covering it.\n"+
				"Read its diff against the fork point first. If it is clearly fork-owned, widen an existing entry's prefix "+
				"or add a new one; if it is a genuine small edit to stock code, add it to otherAllowlist here with a "+
				"one-line reason", f.Path, f.Status)
			continue
		}
		matched[hit] = true
	}

	for i, a := range otherAllowlist {
		if !matched[i] {
			t.Errorf("otherAllowlist entry %q (%q) matches nothing in live/fork-surface.json's other bucket; delete it - "+
				"a stale exemption reads as a live one", a.Prefix, a.Reason)
		}
	}
}

// TestForkSurfaceCountsAgreeWithFiles keeps the artifact's summary counts
// from silently drifting away from the file lists they are supposed to
// describe - the same shape as pins_drift_test.go's provider-pin checks:
// two numbers that started in agreement and are worth failing on the day
// they stop being recomputed together.
func TestForkSurfaceCountsAgreeWithFiles(t *testing.T) {
	doc := loadForkSurface(t)
	for root, files := range doc.Files {
		if got, want := doc.Counts[root], len(files); got != want {
			t.Errorf("live/fork-surface.json: counts[%q] = %d, but files[%q] has %d entries", root, got, root, want)
		}
	}
	for root := range doc.Counts {
		if _, ok := doc.Files[root]; !ok {
			t.Errorf("live/fork-surface.json: counts[%q] has no matching files[%q]", root, root)
		}
	}
}

// TestForkSurfaceHasNoDuplicatePaths catches a bucketing bug that would put
// the same changed path in two roots (or twice in one), which would make
// the per-root counts add up to more files than the diff actually touched.
func TestForkSurfaceHasNoDuplicatePaths(t *testing.T) {
	doc := loadForkSurface(t)
	seen := make(map[string]string, 4096)
	for root, files := range doc.Files {
		for _, f := range files {
			if prevRoot, ok := seen[f.Path]; ok {
				t.Errorf("live/fork-surface.json: %q appears in both %q and %q", f.Path, prevRoot, root)
				continue
			}
			seen[f.Path] = root
		}
	}
}
