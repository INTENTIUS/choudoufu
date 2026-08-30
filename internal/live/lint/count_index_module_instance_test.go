// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// GitHub issue #580's three-case table, plus the three cases the fix has to
// keep refusing, asserted on both sides of the boundary the issue is about:
// what lint says, and what the layer below it actually renders.
//
// Asserting on the lint verdict alone would be the mistake this repository
// has made six times - a predicate can read green while the marker is
// wrong - so every fixture that lint admits is also resolved here and its
// rendered identities compared against the values stock OpenTofu names.
// identity is already an import of this package, so this costs one call and
// no new dependency.

// countIndexRefusals is every RuleCountIndex issue lint raises for a
// fixture directory, in report order.
func countIndexRefusals(t *testing.T, dir string) ([]Issue, *configs.Config) {
	t.Helper()
	cfg := loadConfigDir(t, dir)
	var got []Issue
	for _, iss := range CheckContext(t.Context(), cfg) {
		if iss.Rule == RuleCountIndex {
			got = append(got, iss)
		}
	}
	return got, cfg
}

// resolvedImportIDs is what internal/live/identity renders for a
// configuration, address to import ID, together with whether resolution
// raised the collision refusal.
func resolvedImportIDs(t *testing.T, cfg *configs.Config) (map[string]string, bool) {
	t.Helper()
	result, diags := identity.ResolveWith(t.Context(), cfg, identity.Context{})
	collided := false
	for _, diag := range diags {
		if strings.Contains(diag.Description().Summary, "same identity") {
			collided = true
		}
	}
	ids := map[string]string{}
	if result != nil {
		for _, res := range result.All() {
			ids[res.Addr.String()] = res.ImportID
		}
	}
	return ids, collided
}

// TestCountIndexAcrossAnExpandedModuleCall is issue #580's case A: a module
// called with for_each, whose identity-bearing argument reads each.key at
// the call site, count-expanding inside. Stock plans it; this pass refused
// it, and the values it said it could not compute are these.
func TestCountIndexAcrossAnExpandedModuleCall(t *testing.T) {
	got, cfg := countIndexRefusals(t, "testdata/count-index-module-foreach")
	if len(got) != 0 {
		t.Fatalf("want no %s issue, got %d: %s", RuleCountIndex, len(got), summarizeIssues(got))
	}

	ids, collided := resolvedImportIDs(t, cfg)
	if collided {
		t.Errorf("resolution reported a collision for a configuration whose eight names are distinct")
	}
	want := map[string]string{
		`module.m["pod-a"].aws_iam_role.pod_role[0]`: "tl-pod-a-team-0000-role",
		`module.m["pod-a"].aws_iam_role.pod_role[1]`: "tl-pod-a-team-0001-role",
		`module.m["pod-a"].aws_iam_role.pod_role[2]`: "tl-pod-a-team-0002-role",
		`module.m["pod-a"].aws_iam_role.pod_role[3]`: "tl-pod-a-team-0003-role",
		`module.m["pod-b"].aws_iam_role.pod_role[0]`: "tl-pod-b-team-0000-role",
		`module.m["pod-b"].aws_iam_role.pod_role[1]`: "tl-pod-b-team-0001-role",
		`module.m["pod-b"].aws_iam_role.pod_role[2]`: "tl-pod-b-team-0002-role",
		`module.m["pod-b"].aws_iam_role.pod_role[3]`: "tl-pod-b-team-0003-role",
	}
	assertImportIDs(t, want, ids)
}

// TestCountIndexStillRefusesACollisionInsideAnExpandedModule is the other
// half of the same change, and the half a widening is most likely to break:
// the fix renders these values instead of giving up on them, and what it
// sees is a real collision inside every module instance.
//
// The control directly below is the same fixture with only the duplicate
// entries removed, which is what makes this a proof that the refusal is
// about the values rather than about the module boundary.
func TestCountIndexStillRefusesACollisionInsideAnExpandedModule(t *testing.T) {
	got, _ := countIndexRefusals(t, "testdata/count-index-module-foreach-collides")
	if len(got) != 1 {
		t.Fatalf("want exactly 1 %s issue, got %d: %s", RuleCountIndex, len(got), summarizeIssues(got))
	}
	if want := "count.index in aws_iam_role.pod_role"; got[0].Construct != want {
		t.Errorf("issue names %q, want %q", got[0].Construct, want)
	}
	// The COLLIDES wording, not the "cannot be computed here" wording: the
	// values were seen, at the count this configuration declares, and two
	// of them are equal. Reporting the unprovable sentence here would send
	// an operator to supply a variable that is already supplied.
	if !strings.Contains(got[0].Detail, "render the SAME value") {
		t.Errorf("issue reports the unprovable wording where a collision was demonstrated:\n%s", got[0].Detail)
	}
}

// TestCountIndexStillRefusesAnUnprovableValueInsideAnExpandedModule is the
// second unsafe case: inside #580's widened admission, but reading a
// managed resource's attribute, which no static evaluation can know before
// an apply.
func TestCountIndexStillRefusesAnUnprovableValueInsideAnExpandedModule(t *testing.T) {
	got, _ := countIndexRefusals(t, "testdata/count-index-module-foreach-unprovable")
	if len(got) != 1 {
		t.Fatalf("want exactly 1 %s issue, got %d: %s", RuleCountIndex, len(got), summarizeIssues(got))
	}
	if !strings.Contains(got[0].Detail, "cannot be computed here") {
		t.Errorf("issue reports the collision wording where nothing was rendered:\n%s", got[0].Detail)
	}
}

// TestCountIndexAdmitsTheControlForBothRefusals removes exactly one
// obstacle from each of the two fixtures above - the duplicate entries from
// one, the resource-derived prefix from the other - and asserts that what
// is left resolves. Without this the two tests above prove only that
// something refuses, not that it refuses for the reason it names.
func TestCountIndexAdmitsTheControlForBothRefusals(t *testing.T) {
	got, cfg := countIndexRefusals(t, "testdata/count-index-module-foreach-distinct")
	if len(got) != 0 {
		t.Fatalf("want no %s issue, got %d: %s", RuleCountIndex, len(got), summarizeIssues(got))
	}
	ids, collided := resolvedImportIDs(t, cfg)
	if collided {
		t.Errorf("resolution reported a collision for a configuration whose eight names are distinct")
	}
	want := map[string]string{
		`module.m["pod-a"].aws_iam_role.pod_role[0]`: "tl-pod-a-blue-role",
		`module.m["pod-a"].aws_iam_role.pod_role[1]`: "tl-pod-a-green-role",
		`module.m["pod-a"].aws_iam_role.pod_role[2]`: "tl-pod-a-amber-role",
		`module.m["pod-a"].aws_iam_role.pod_role[3]`: "tl-pod-a-violet-role",
		`module.m["pod-b"].aws_iam_role.pod_role[0]`: "tl-pod-b-blue-role",
		`module.m["pod-b"].aws_iam_role.pod_role[1]`: "tl-pod-b-green-role",
		`module.m["pod-b"].aws_iam_role.pod_role[2]`: "tl-pod-b-amber-role",
		`module.m["pod-b"].aws_iam_role.pod_role[3]`: "tl-pod-b-violet-role",
	}
	assertImportIDs(t, want, ids)
}

// TestCrossModuleInstanceCollisionIsStillRefusedBelow pins the boundary of
// what the count.index rule answers, on the two shapes where a module call
// passes every instance the same identity-bearing value.
//
// count-index admits both: within either module instance the four indices
// render four different names. The collision is BETWEEN module instances,
// which is a collision between two whole rendered identities, and
// internal/live/identity's own checkCollisions is the pass that compares
// those. "shared" reaches this state without #580's fix (its call reads no
// each.key, so the frozen closure evaluates it); "flattened" reads each.key
// and throws it away, so it is reachable only BECAUSE of #580's fix, and is
// the one shape the widened admission newly depends on that refusal for.
//
// If this test ever fails, the widened admission is writing a wrong marker
// and #580's fix has to grow a cross-instance distinctness check of its
// own.
func TestCrossModuleInstanceCollisionIsStillRefusedBelow(t *testing.T) {
	for _, dir := range []string{
		"testdata/count-index-module-foreach-shared",
		"testdata/count-index-module-foreach-flattened",
	} {
		t.Run(dir, func(t *testing.T) {
			got, cfg := countIndexRefusals(t, dir)
			if len(got) != 0 {
				t.Fatalf("want no %s issue (the collision is between module instances, not between indices), got %d: %s",
					RuleCountIndex, len(got), summarizeIssues(got))
			}
			ids, collided := resolvedImportIDs(t, cfg)
			if !collided {
				t.Fatalf("resolution did NOT refuse a configuration where two module instances render one identity; rendered: %s", sortedIDs(ids))
			}
			// Named rather than merely counted: the two instances have to
			// render the SAME string for the refusal below to be the one
			// this test claims is catching it.
			a := ids[`module.m["pod-a"].aws_iam_role.pod_role[0]`]
			b := ids[`module.m["pod-b"].aws_iam_role.pod_role[0]`]
			if a == "" || a != b {
				t.Errorf("the two module instances render %q and %q; this fixture only tests what it claims when they are equal and non-empty", a, b)
			}
		})
	}
}

// TestCountIndexModuleInstancesAreEnumeratedPerCall is the direct unit
// check on the new machinery: a module called with for_each has one
// evaluator per instance, and a module reached through nothing but static
// calls keeps the frozen closure it always had.
func TestCountIndexModuleInstancesAreEnumeratedPerCall(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/count-index-module-foreach")
	child, ok := cfg.Children["m"]
	if !ok {
		t.Fatal("fixture has no module.m")
	}

	evals, ok := moduleInstanceEvaluators(t.Context(), child)
	if !ok {
		t.Fatal("moduleInstanceEvaluators declined a for_each'd call whose keys are two literal strings")
	}
	if len(evals) != 2 {
		t.Errorf("got %d evaluators for a call with two keys, want 2", len(evals))
	}

	// The root module has no caller and one instance, so it keeps its own
	// evaluator and this reports false - which is what leaves every
	// configuration without an expanded module call on exactly the path it
	// was on before this file existed.
	if _, ok := moduleInstanceEvaluators(t.Context(), cfg); ok {
		t.Error("moduleInstanceEvaluators rebuilt the ROOT module's closure; it has no caller to rebuild it from")
	}
}

func assertImportIDs(t *testing.T, want, got map[string]string) {
	t.Helper()
	for addr, wantID := range want {
		gotID, ok := got[addr]
		if !ok {
			t.Errorf("%s did not resolve at all; resolved: %s", addr, sortedIDs(got))
			continue
		}
		if gotID != wantID {
			t.Errorf("%s resolved to %q, want %q", addr, gotID, wantID)
		}
	}
	if len(got) != len(want) {
		t.Errorf("resolved %d instances, want %d: %s", len(got), len(want), sortedIDs(got))
	}
}

func sortedIDs(ids map[string]string) string {
	keys := make([]string, 0, len(ids))
	for k := range ids {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+ids[k])
	}
	return strings.Join(parts, ", ")
}
