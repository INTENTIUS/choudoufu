// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

// TestEveryNeedsDiscoveryReturnCarriesACause reads this package's own source
// and requires every composite literal that classifies ClassNeedsDiscovery to
// set Cause in the same literal.
//
// A fixture sweep would not do this job. It can only report on the causes the
// fixtures happen to reach, so a sixth classification site added later - or an
// existing one moved into a new branch - would resolve with the zero cause,
// [DiscoveryCause.Normalize] would turn it into UNSPECIFIED, and stamping
// would print the server-assigned sentence over it. That is precisely the
// failure this type was introduced to end, and it would come back silently.
// The source is the external thing this test consults, in the sense the
// repository's ratchet rule asks for: mutating the derivation rule cannot make
// this test agree with it.
func TestEveryNeedsDiscoveryReturnCarriesACause(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %s", err)
	}

	fset := token.NewFileSet()
	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %s", name, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			keys := map[string]bool{}
			needsDiscovery := false
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				keys[key.Name] = true
				if key.Name != "Class" {
					continue
				}
				if val, ok := kv.Value.(*ast.Ident); ok && val.Name == "ClassNeedsDiscovery" {
					needsDiscovery = true
				}
			}
			if !needsDiscovery {
				return true
			}
			found++
			if !keys["Cause"] {
				t.Errorf("%s: a resolution classified ClassNeedsDiscovery with no Cause set. "+
					"Every classification site must say which of the situations in DiscoveryCause it is, "+
					"or internal/live/stamp prints the server-assigned sentence over it.",
					fset.Position(lit.Pos()))
			}
			return true
		})
	}

	// The count is not the assertion, but a run that inspected nothing would
	// pass vacuously - the completeness-test-that-could-see-almost-nothing
	// shape this repository has caught twice.
	if found == 0 {
		t.Fatal("no ClassNeedsDiscovery classification sites found at all; this test can no longer see what it guards")
	}
	t.Logf("%d ClassNeedsDiscovery classification sites, all carrying a cause", found)
}

// TestDiscoveryCausesByBlock_disagreementCollapses covers the fold from
// instances to blocks. One configuration body serves every instance a
// for_each expands to, but the instances can reach different causes - a name
// argument that is null for one key and set for another. A block whose
// instances disagree must not be described with whichever one was walked
// first.
func TestDiscoveryCausesByBlock_disagreementCollapses(t *testing.T) {
	inst := func(name string, key addrs.InstanceKey, cause DiscoveryCause, args ...string) Resolution {
		return Resolution{
			Addr: addrs.AbsResourceInstance{
				Module: addrs.RootModuleInstance,
				Resource: addrs.ResourceInstance{
					Resource: addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_iam_role_policy", Name: name},
					Key:      key,
				},
			},
			Class:     ClassNeedsDiscovery,
			Cause:     cause,
			CauseArgs: args,
		}
	}

	instances := []Resolution{
		// Agreeing instances keep their cause and their subjects.
		inst("agree", addrs.StringKey("a"), DiscoveryNameOmitted, "name"),
		inst("agree", addrs.StringKey("b"), DiscoveryNameOmitted, "name"),
		// Same cause, different subject: still a disagreement, because the
		// sentence names the subject.
		inst("subjects", addrs.StringKey("a"), DiscoveryNameOmitted, "name"),
		inst("subjects", addrs.StringKey("b"), DiscoveryNameOmitted, "statement_id"),
		// Different causes.
		inst("causes", addrs.StringKey("a"), DiscoveryNameOmitted, "name"),
		inst("causes", addrs.StringKey("b"), DiscoveryServerAssigned),
		// A caller-assembled resolution with no cause at all.
		inst("bare", addrs.NoKey, ""),
	}

	res := newResult()
	for _, r := range instances {
		res.add(r)
	}
	got := res.DiscoveryCausesByBlock()

	want := map[string]BlockDiscovery{
		"aws_iam_role_policy.agree":    {Cause: DiscoveryNameOmitted, Args: []string{"name"}},
		"aws_iam_role_policy.subjects": {Cause: DiscoveryCauseUnspecified},
		"aws_iam_role_policy.causes":   {Cause: DiscoveryCauseUnspecified},
		"aws_iam_role_policy.bare":     {Cause: DiscoveryCauseUnspecified},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d blocks, want %d: %v", len(got), len(want), got)
	}
	for key, expect := range want {
		actual, ok := got[key]
		if !ok {
			t.Errorf("block %s is missing from the fold", key)
			continue
		}
		if !actual.sameAs(expect) {
			t.Errorf("block %s folded to %+v, want %+v", key, actual, expect)
		}
	}

	// The fold has to be order-independent, because Resolutions arrive in
	// whatever order the walk produced and "the first one wins" is exactly
	// the defect this collapse exists to prevent. Reversing the slice must
	// not change any answer.
	reversed := newResult()
	for i := len(instances) - 1; i >= 0; i-- {
		reversed.add(instances[i])
	}
	backwards := reversed.DiscoveryCausesByBlock()
	for key, forward := range got {
		back, ok := backwards[key]
		if !ok || !back.sameAs(forward) {
			t.Errorf("block %s folded to %+v forwards and %+v backwards", key, forward, back)
		}
	}
}

// TestDiscoveryCauseNormalize pins the one property every reader depends on:
// the zero value is not a cause, and every real cause is left alone.
func TestDiscoveryCauseNormalize(t *testing.T) {
	if got := DiscoveryCause("").Normalize(); got != DiscoveryCauseUnspecified {
		t.Errorf("the zero cause normalized to %q, want %q", got, DiscoveryCauseUnspecified)
	}
	for _, cause := range AllDiscoveryCauses() {
		if cause == "" {
			t.Error("AllDiscoveryCauses contains the empty string, which a map lookup cannot tell from an absent key")
		}
		if got := cause.Normalize(); got != cause {
			t.Errorf("%q normalized to %q", cause, got)
		}
	}
}
