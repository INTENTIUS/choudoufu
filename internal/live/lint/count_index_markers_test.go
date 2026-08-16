// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestCountIndexAdmittedShapesRenderDistinctIdentities is the test that
// decides whether this rule is safe, and it deliberately does not ask the
// analyzer anything. analyzeCountIndexSafety said "safe" for
// 100 + (count.index % 3) once (GitHub issue #192, reopened), and the
// reason that was allowed to ship is that every test around it asked the
// analyzer whether it was happy rather than asking what the instances
// actually resolved to. So this one resolves them.
//
// The external source it consults is internal/live/identity's own Resolve:
// the same resolution the live path uses to decide which cloud object an
// instance owns, over the same fixture, with no reference to count_index.go
// or anything in it. Mutating the analyzer to agree with itself cannot make
// this test pass; only the identities coming out distinct can.
//
// Both directions are checked, because only checking one is how a rule ends
// up either unsound or useless:
//
//   - Every resource the rule ADMITS must resolve to a distinct identity at
//     every instance. A duplicate here is a wrong marker on a real cloud
//     resource - two instances claiming one object - which is the worst
//     outcome this tool has.
//   - Every resource the rule REFUSES must actually collide (or fail to
//     resolve at all, which is a different and equally legitimate reason to
//     refuse). Without this half a rule that refused everything would be
//     green, which is how a conservative rule becomes one that refuses
//     working configurations.
//
// Both halves run over all three fixtures rather than assuming which
// fixture holds which, so the test measures the rule and not the filenames.
func TestCountIndexAdmittedShapesRenderDistinctIdentities(t *testing.T) {
	// All three fixtures, not just the one whose name says "safe". The
	// invariant is a property of the RULE - whatever it admits resolves
	// distinctly - so it has to be checked wherever the rule admits
	// something, and the fixture of shapes chosen to be refused is exactly
	// where an over-permissive rule would first admit one by mistake.
	dirs := []string{
		"testdata/count-index-pure-scalar",
		"testdata/count-index",
		"testdata/count-index-nonlinear",
	}

	for _, dir := range dirs {
		t.Run(dir, func(t *testing.T) {
			cfg := loadConfigDir(t, dir)
			refused := refusedResources(t, cfg)
			byResource := importIDsByResource(t, dir, cfg)

			var admitted, collided int
			for addr, ids := range byResource {
				if len(ids) < 2 {
					// One instance cannot collide with anything, so it is
					// evidence either way and is skipped rather than
					// counted.
					continue
				}
				_, isDup := firstDuplicate(ids)
				if refused[addr] {
					collided++
					if !isDup {
						t.Errorf("%s: refused by the count-index rule, but its %d instances resolve to distinct identities %v - the rule is refusing a configuration that works", addr, len(ids), ids)
					}
					continue
				}
				admitted++
				if isDup {
					dup, _ := firstDuplicate(ids)
					t.Errorf("%s: ADMITTED by the count-index rule, but two instances resolve to the same live identity %q, from %v - this is a wrong marker on a real cloud resource", addr, dup, ids)
				}
			}

			if admitted+collided == 0 {
				t.Fatalf("%s: no resource resolved two or more concrete instances, so this fixture proved nothing", dir)
			}
		})
	}

	// The fixture split itself, asserted here rather than left implicit, so
	// that this test alone says which side of the rule each fixture is on.
	// Without it, a rule that refused everything would satisfy every check
	// above.
	t.Run("the safe fixture is refused nothing and the others are refused something", func(t *testing.T) {
		if got := refusedResources(t, loadConfigDir(t, "testdata/count-index-pure-scalar")); len(got) != 0 {
			t.Errorf("testdata/count-index-pure-scalar: want no count-index refusal, got %v", sortedKeys(got))
		}
		for _, dir := range []string{"testdata/count-index", "testdata/count-index-nonlinear"} {
			if got := refusedResources(t, loadConfigDir(t, dir)); len(got) == 0 {
				t.Errorf("%s: want at least one count-index refusal, got none", dir)
			}
		}
	})
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// importIDsByResource resolves every instance in a fixture and groups the
// concrete import identities by the resource they belong to, dropping the
// instance key. Only ClassConcrete resolutions carry an identity string a
// collision could be observed in; anything else refused earlier, for a
// reason that is not this rule's.
func importIDsByResource(t *testing.T, dir string, cfg *configs.Config) map[string][]string {
	t.Helper()

	result, diags := identity.Resolve(t.Context(), cfg)
	if result == nil {
		t.Fatalf("resolving %s produced no result: %s", dir, diags.Err())
	}

	byResource := make(map[string][]string)
	for _, res := range result.All() {
		if res.Class != identity.ClassConcrete || res.ImportID == "" {
			continue
		}
		addr := res.Addr.ContainingResource().String()
		byResource[addr] = append(byResource[addr], res.ImportID)
	}
	for addr := range byResource {
		sort.Strings(byResource[addr])
	}
	return byResource
}

// refusedResources is the set of resource addresses this package's own
// count-index rule reported an issue for in a fixture, keyed the same way
// importIDsByResource keys its groups.
func refusedResources(t *testing.T, cfg *configs.Config) map[string]bool {
	t.Helper()

	refused := make(map[string]bool)
	for _, issue := range CheckContext(t.Context(), cfg) {
		if issue.Rule != RuleCountIndex {
			continue
		}
		// Construct reads "count.index in aws_network_acl_rule.modulo".
		const prefix = "count.index in "
		if len(issue.Construct) > len(prefix) {
			refused[issue.Construct[len(prefix):]] = true
		}
	}
	return refused
}

// firstDuplicate reports the first value appearing more than once in a
// sorted slice.
func firstDuplicate(sorted []string) (string, bool) {
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return sorted[i], true
		}
	}
	return "", false
}
