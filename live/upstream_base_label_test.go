// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// This file is issue #1778's guard (ruling 7) on how this fork names the
// upstream OpenTofu it is built on.
//
// Every CHANGELOG entry v0.1.0-v0.20.0 said "Built on OpenTofu 1.13.0", and
// every published release note said "stock OpenTofu 1.13.0". The base is
// upstream main at 03743ce6e8, whose version/VERSION reads 1.13.0-dev: an
// unreleased tree, not the 1.13.0 release. Two places manufactured the
// wrong number by stripping the suffix: release.yml's
// `sed 's/-dev$//'` and tools/forkdiff-gen's readBaseOpenTofuVersion, which
// kept only the release core.
//
// The authority here is the fork point's own version/VERSION, verbatim. It
// is read without git: live/fork-surface.json lists every path that differs
// between HEAD and the fork point, so when version/VERSION is absent from
// that list, HEAD's copy is byte-identical to the fork point's. A CI
// checkout that lacks the fork-point object (history here was re-rooted on
// 2026-08-14) can still check it, and nothing is skipped.

const (
	upstreamVersionFile = "../version/VERSION"
	changelogFile       = "../CHANGELOG.md"
	releaseWorkflowFile = "../.github/workflows/release.yml"
)

// forkSurfaceBaseDoc decodes the fork-surface fields this file needs.
type forkSurfaceBaseDoc struct {
	ForkPointShort      string                       `json:"fork_point_short"`
	BaseOpenTofuVersion string                       `json:"base_opentofu_version"`
	Files               map[string][]forkSurfaceFile `json:"files"`
}

// forkPointUpstreamVersion returns the fork point's version/VERSION,
// trimmed of surrounding whitespace and nothing else, plus the decoded
// artifact.
func forkPointUpstreamVersion(t *testing.T) (string, forkSurfaceBaseDoc) {
	t.Helper()
	var doc forkSurfaceBaseDoc
	decodeInto(t, "fork-surface.json", &doc)
	for _, files := range doc.Files {
		for _, f := range files {
			if f.Path == "version/VERSION" {
				t.Fatalf("live/fork-surface.json lists version/VERSION as %s against the fork point, so HEAD's "+
					"copy no longer equals the fork point's and this guard cannot read the base from the tree. "+
					"Either the fork point moved (re-run `go run ./tools/forkdiff-gen`) or version/VERSION was "+
					"hand-edited, which this fork does not do: it reports upstream's version unchanged", f.Status)
			}
		}
	}
	raw, err := os.ReadFile(upstreamVersionFile)
	if err != nil {
		t.Fatalf("reading %s: %v", upstreamVersionFile, err)
	}
	v := strings.TrimSpace(string(raw))
	if v == "" {
		t.Fatalf("%s is empty", upstreamVersionFile)
	}
	return v, doc
}

// TestForkSurfaceBaseVersionIsTheForkPointsOwn holds base_opentofu_version
// to the fork point's version/VERSION exactly, prerelease suffix included.
func TestForkSurfaceBaseVersionIsTheForkPointsOwn(t *testing.T) {
	want, doc := forkPointUpstreamVersion(t)
	if doc.BaseOpenTofuVersion != want {
		t.Errorf("live/fork-surface.json records base_opentofu_version %q, but the fork point %s's "+
			"version/VERSION reads %q. The base is named verbatim, -dev included: a -dev tree is unreleased "+
			"upstream main, not the release it is heading toward (#1778). Re-run `go run ./tools/forkdiff-gen` "+
			"and `go run ./tools/forkdiff-gen -render`",
			doc.BaseOpenTofuVersion, doc.ForkPointShort, want)
	}
}

// changelogBuiltOnRe is the one accepted shape of a CHANGELOG "Built on"
// claim: the upstream version, then the upstream ref and fork-point commit
// it was taken from, so the number can never again stand without the
// commit that settles it.
var changelogBuiltOnRe = regexp.MustCompile("Built on OpenTofu ([^ ]+) \\(upstream [^`()]+ `([0-9a-f]{10})`\\)")

// changelogPastBases maps an earlier fork point's short hash to its own
// version/VERSION. Empty while every release shares one fork point; when
// the base moves (#1778's port), the outgoing pair is added here so the
// historical entries keep checking against what they were built on.
var changelogPastBases = map[string]string{}

// TestChangelogBuiltOnLinesNameTheForkPointVersion holds every CHANGELOG
// "Built on OpenTofu" claim to the version/VERSION of the fork point it
// names.
func TestChangelogBuiltOnLinesNameTheForkPointVersion(t *testing.T) {
	base, doc := forkPointUpstreamVersion(t)
	raw, err := os.ReadFile(changelogFile)
	if err != nil {
		t.Fatalf("reading %s: %v", changelogFile, err)
	}
	seen := 0
	for i, line := range strings.Split(string(raw), "\n") {
		claims := strings.Count(line, "Built on OpenTofu")
		if claims == 0 {
			continue
		}
		seen += claims
		ms := changelogBuiltOnRe.FindAllStringSubmatch(line, -1)
		if len(ms) != claims {
			t.Errorf("CHANGELOG.md:%d: a \"Built on OpenTofu\" claim without its fork point; write it as "+
				"\"Built on OpenTofu %s (upstream main `%s`)\":\n\t%s", i+1, base, doc.ForkPointShort, line)
			continue
		}
		for _, m := range ms {
			got, commit := m[1], m[2]
			want, ok := changelogPastBases[commit]
			if commit == doc.ForkPointShort {
				want, ok = base, true
			}
			if !ok {
				t.Errorf("CHANGELOG.md:%d names fork point %s, which is neither live/fork-surface.json's "+
					"fork_point_short (%s) nor in changelogPastBases", i+1, commit, doc.ForkPointShort)
				continue
			}
			if got != want {
				t.Errorf("CHANGELOG.md:%d says \"Built on OpenTofu %s\", but fork point %s's version/VERSION "+
					"reads %q (#1778: the -dev suffix is part of the name)", i+1, got, commit, want)
			}
		}
	}
	if seen == 0 {
		t.Fatalf("%s carries no \"Built on OpenTofu\" claim at all; this guard has nothing to read", changelogFile)
	}
}

// TestReleaseNotesTakeTheBaseFromForkSurface holds release.yml to reading
// the base version and the fork-point commit from live/fork-surface.json,
// rather than stripping version/VERSION or typing the hash.
func TestReleaseNotesTakeTheBaseFromForkSurface(t *testing.T) {
	_, doc := forkPointUpstreamVersion(t)
	raw, err := os.ReadFile(releaseWorkflowFile)
	if err != nil {
		t.Fatalf("reading %s: %v", releaseWorkflowFile, err)
	}
	body := string(raw)
	if strings.Contains(body, "-dev$") {
		t.Errorf("%s strips the -dev suffix from the upstream version; the release notes must name the "+
			"fork point's version/VERSION verbatim (#1778)", releaseWorkflowFile)
	}
	if strings.Contains(body, doc.ForkPointShort) {
		t.Errorf("%s types the fork-point hash %s as a literal; read fork_point_short from "+
			"live/fork-surface.json so the notes follow the base when it moves", releaseWorkflowFile, doc.ForkPointShort)
	}
	for _, want := range []string{"live/fork-surface.json", "base_opentofu_version", "fork_point_short"} {
		if !strings.Contains(body, want) {
			t.Errorf("%s does not mention %s; the release notes' base must come from live/fork-surface.json",
				releaseWorkflowFile, want)
		}
	}
}
