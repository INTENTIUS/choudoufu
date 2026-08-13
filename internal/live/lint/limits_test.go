// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// limitsDir is the root of the limits wing (PE.1), relative to this package.
// It lives outside internal/live/lint, at the repository root, the same
// way the P0.1 estate fixture does: it is an e2e fixture, not a lint fixture,
// even though this file is the only place that walks it today.
func limitsDir(t *testing.T) string {
	return flocitest.LimitsDir(t)
}

// limitationsDoc is live/LIMITATIONS.md, read by TestLimitationsDocCoversDirs
// to keep the doc, the fixtures, and this table from drifting apart silently.
const limitationsDoc = "../../../live/LIMITATIONS.md"

// enforcedLimits is every live/e2e/limits directory whose construct is
// rejected by lint today, paired with the exact rule that must fire. CheckContext()
// must report that rule, and only that rule, for each of these directories.
var enforcedLimits = map[string]Rule{
	"local-exec":         RuleProvisioner,
	"remote-exec":        RuleProvisioner,
	"null-resource":      RuleLogicalResource,
	"terraform-data":     RuleLogicalResource,
	"local-file":         RuleLogicalResource,
	"random-password":    RuleLogicalResource,
	"time-sleep":         RuleLogicalResource,
	"remote-state":       RuleRemoteState,
	"moved-block":        RuleMovedBlock,
	"child-module":       RuleChildModule,
	"backend-block":      RuleStateBackend,
	"cloud-block":        RuleStateBackend,
	"unadmitted-type":    RuleUnadmittedType,
	"count-index-in-tag": RuleCountIndex,
	"foreach-dotted-key": RuleForEachKey,
	"overlong-address":   RuleOverlongAddress,
}

// notYetEnforcedLimits is every live/e2e/limits directory whose
// construct is banned or bounded by the roadmap but has no lint rule yet.
// CheckContext() must report nothing for these today. This is a pin, not an
// endorsement: the day a rule lands for one of these, this test starts
// failing loudly (Check no longer returns zero issues), and the fix is to
// move the directory into enforcedLimits and update LIMITATIONS.md in the
// same change, not to relax the assertion.
// RA.3 moved foreach-dotted-key out of this list and into enforcedLimits:
// RuleForEachKey (internal/live/lint/foreach_key.go) now rejects a key
// carrying "." or ":" or anything else outside the AWS tag-value set.
// overlong-address followed the same path: RuleOverlongAddress
// (internal/live/lint/overlong_address.go) now measures the escaped
// address against MARKERS.md's 256-character cap.
var notYetEnforcedLimits = []string{
	"duplicate-identity", // enforced at resolve time (internal/live/identity), not lint
}

// TestLimitsEnforced checks that every directory in enforcedLimits fails lint
// with exactly its named rule, and nothing else.
func TestLimitsEnforced(t *testing.T) {
	for dir, rule := range enforcedLimits {
		t.Run(dir, func(t *testing.T) {
			cfg := loadConfigDir(t, filepath.Join(limitsDir(t), dir))
			issues := CheckContext(t.Context(), cfg)

			if len(issues) == 0 {
				t.Fatalf("CheckContext() reported no issues for %s, want at least one %s", dir, rule)
			}
			for _, issue := range issues {
				if issue.Rule != rule {
					t.Errorf("%s: got rule %q, want exactly %q (and no other rule): %s", dir, issue.Rule, rule, issue)
				}
			}
		})
	}
}

// logicalLimitsClasses pairs every logical-resource limits fixture with the
// [LogicalClass] its one resource classifies as, so
// TestLogicalLimitsDetailsRender can check that the class actually shows up
// in the Detail an operator sees - not just that RuleLogicalResource fired
// (TestLimitsEnforced already pins that), but that the new per-type
// classification (GitHub issue #73's groundwork) reached the message.
var logicalLimitsClasses = map[string]LogicalClass{
	"null-resource":   ClassRecordAdmitted,
	"terraform-data":  ClassRecordAdmitted,
	"time-sleep":      ClassRecordAdmitted,
	"random-password": ClassSecretRefused,
	"local-file":      ClassOtherRefused,
}

// TestLogicalLimitsDetailsRender checks that every logical-resource limits
// fixture's single issue carries the right class-specific wording in its
// Detail: RECORD_ADMITTED and SECRET_REFUSED name their class outright and
// carry the forwarding language specific to them (#73, and the secret
// manager address, respectively), while OTHER_REFUSED carries neither by
// design - its whole point is that the original, class-agnostic wording is
// unchanged (TestLogicalResourceDetailsRenderByClass in lint_test.go pins
// that wording byte-identical to the pre-table template).
func TestLogicalLimitsDetailsRender(t *testing.T) {
	for dir, class := range logicalLimitsClasses {
		t.Run(dir, func(t *testing.T) {
			cfg := loadConfigDir(t, filepath.Join(limitsDir(t), dir))
			issues := CheckContext(t.Context(), cfg)

			var logical []Issue
			for _, issue := range issues {
				if issue.Rule == RuleLogicalResource {
					logical = append(logical, issue)
				}
			}
			if len(logical) != 1 {
				t.Fatalf("%s: got %d RuleLogicalResource issues, want exactly 1: %v", dir, len(logical), logical)
			}

			detail := logical[0].Detail
			switch class {
			case ClassRecordAdmitted:
				if !strings.Contains(detail, string(class)) {
					t.Errorf("%s: Detail = %q, want it to contain class %q", dir, detail, class)
				}
				if !strings.Contains(detail, "#73") {
					t.Errorf("%s: RECORD_ADMITTED Detail = %q, want it to cite #73", dir, detail)
				}
			case ClassSecretRefused:
				if !strings.Contains(detail, string(class)) {
					t.Errorf("%s: Detail = %q, want it to contain class %q", dir, detail, class)
				}
				if !strings.Contains(detail, "secret manager") {
					t.Errorf("%s: SECRET_REFUSED Detail = %q, want the secret-manager forwarding address", dir, detail)
				}
			case ClassOtherRefused:
				if strings.Contains(detail, string(ClassRecordAdmitted)) || strings.Contains(detail, string(ClassSecretRefused)) {
					t.Errorf("%s: OTHER_REFUSED Detail = %q, want no other class's wording (current wording preserved)", dir, detail)
				}
			}
		})
	}
}

// childModuleFixtureDetails is the substring each of the refused module
// calls in live/e2e/limits/child-module/main.tf is expected to carry in its
// Detail, keyed by the call's own name. #59 narrowed RuleChildModule to the
// two shapes that stay refused - the same rule fires for a count-expanded
// call and a for_each-expanded call, but each is refused for a different,
// named reason (live/LIMITATIONS.md, "child-module"). The fixture's third
// call, "network", is a static module call: 59b admitted the shape
// entirely, so it carries no RuleChildModule issue at all and is not in
// this table.
var childModuleFixtureDetails = map[string]string{
	"counted": "refused permanently",
	"keyed":   "issue #59, phase 3",
}

// TestChildModuleDichotomy checks that live/e2e/limits/child-module/, which
// carries a static call, a count call and a for_each call in one tree, gets
// exactly two RuleChildModule issues back - one for the count call and one
// for the for_each call, each with the Detail that names its own reason -
// and none at all for the static call, which 59b admitted.
func TestChildModuleDichotomy(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join(limitsDir(t), "child-module"))
	issues := CheckContext(t.Context(), cfg)

	got := make(map[string]Issue, len(issues))
	for _, issue := range issues {
		if issue.Rule != RuleChildModule {
			continue
		}
		// Construct is `module "<name>"`; the name is what
		// childModuleFixtureDetails is keyed by.
		name := strings.TrimSuffix(strings.TrimPrefix(issue.Construct, `module "`), `"`)
		got[name] = issue
	}

	if _, ok := got["network"]; ok {
		t.Errorf("child-module: the static call \"network\" carries a RuleChildModule issue; 59b admitted static module calls")
	}

	if len(got) != len(childModuleFixtureDetails) {
		t.Fatalf("child-module: got %d distinct RuleChildModule module calls %v, want exactly %d: %v", len(got), got, len(childModuleFixtureDetails), childModuleFixtureDetails)
	}

	for name, wantSubstring := range childModuleFixtureDetails {
		issue, ok := got[name]
		if !ok {
			t.Errorf("child-module: no issue reported for module %q", name)
			continue
		}
		if !strings.Contains(issue.Detail, wantSubstring) {
			t.Errorf("child-module: module %q Detail = %q, want it to contain %q", name, issue.Detail, wantSubstring)
		}
	}
}

// TestLimitsNotYetEnforced checks that every directory in notYetEnforcedLimits
// produces no issues today. This pins current behavior on purpose: these
// constructs ARE banned or bounded by the roadmap, but no lint rule exists to
// catch them yet. When one lands, this assertion fails, which is the point —
// silence here would mean the gap stayed hidden.
func TestLimitsNotYetEnforced(t *testing.T) {
	for _, dir := range notYetEnforcedLimits {
		t.Run(dir, func(t *testing.T) {
			cfg := loadConfigDir(t, filepath.Join(limitsDir(t), dir))
			issues := CheckContext(t.Context(), cfg)
			if len(issues) != 0 {
				t.Errorf("CheckContext() reported %d issues for %s, want 0 (documented, not yet enforced); "+
					"if a rule now catches this, move %s into enforcedLimits and update live/LIMITATIONS.md",
					len(issues), dir, dir)
				for _, issue := range issues {
					t.Logf("  got: %s", issue)
				}
			}
		})
	}
}

// TestLimitsDirsMatchTable is the completeness check: every subdirectory of
// live/e2e/limits must appear in exactly one of enforcedLimits or
// notYetEnforcedLimits, and every entry in those two tables must correspond
// to a real directory. Either direction of mismatch means the fixture tree
// and this test drifted apart.
func TestLimitsDirsMatchTable(t *testing.T) {
	entries, err := os.ReadDir(limitsDir(t))
	if err != nil {
		t.Fatalf("reading %s: %s", limitsDir(t), err)
	}

	actual := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			actual[entry.Name()] = true
		}
	}

	expected := make(map[string]bool)
	for dir := range enforcedLimits {
		expected[dir] = true
	}
	for _, dir := range notYetEnforcedLimits {
		if expected[dir] {
			t.Errorf("%s appears in both enforcedLimits and notYetEnforcedLimits", dir)
		}
		expected[dir] = true
	}

	for dir := range actual {
		if !expected[dir] {
			t.Errorf("live/e2e/limits/%s exists but is not in enforcedLimits or notYetEnforcedLimits in this test", dir)
		}
	}
	for dir := range expected {
		if !actual[dir] {
			t.Errorf("%s is listed in this test but live/e2e/limits/%s does not exist", dir, dir)
		}
	}
}

// TestLimitationsDocCoversDirs checks that live/LIMITATIONS.md has an
// entry for every limits directory, and names no directory that does not
// exist. The doc is kept simple enough for this to work as a plain substring
// search: every entry in LIMITATIONS.md is required to head its section with
// "### <dir-name>", matching the directory name under live/e2e/limits
// exactly, so doc, fixtures, and this table cannot drift apart silently.
func TestLimitationsDocCoversDirs(t *testing.T) {
	content, err := os.ReadFile(limitationsDoc)
	if err != nil {
		t.Fatalf("reading %s: %s", limitationsDoc, err)
	}
	doc := string(content)

	var allDirs []string
	for dir := range enforcedLimits {
		allDirs = append(allDirs, dir)
	}
	allDirs = append(allDirs, notYetEnforcedLimits...)
	sort.Strings(allDirs)

	for _, dir := range allDirs {
		heading := "### " + dir
		if !strings.Contains(doc, heading) {
			t.Errorf("live/LIMITATIONS.md has no %q heading for live/e2e/limits/%s", heading, dir)
		}
	}

	// The other direction: every "### " heading in the doc should name a
	// directory this test knows about, so a stale doc entry (a renamed or
	// removed fixture) is caught too.
	known := make(map[string]bool)
	for _, dir := range allDirs {
		known[dir] = true
	}
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "### ") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "### "))
		if !known[name] {
			t.Errorf("live/LIMITATIONS.md heading %q does not match any known limits directory", line)
		}
	}
}
