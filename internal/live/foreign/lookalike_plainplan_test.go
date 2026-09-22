// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
)

// TestLookalikeGuardOnAPlainPlanReport is this package's half of GitHub
// issue #1480: the seam between what a plain plan's discovery pass hands
// over and whether the guard can say anything at all.
//
// The two subtests are the same estate at the same moment - the estate's own
// aws_security_group.main stripped of its tofu-estate and tofu-address tags,
// so the plan proposes creating it - under the two discovery Reports a plain
// plan produced before and after #1480. Nothing in THIS package changed
// between them; what changed is whether the stripped group crossed the wire
// at all, which is why the fix is in internal/live/discovery
// ([discovery.relistForLookalikes]) and the red test that drives it is
// TestLookalikeRelistOnPlainPlan over there.
//
// It is here because the shape of that hand-over is a contract with two
// sides, and both of the things this package does with it are load-bearing:
// the warning itself, and the coverage sentence printed above it. A report
// that said ScopeEstate would have this package print "aws_security_group
// was listed with the server-side estate filter on, so ... unclaimed ones
// were never visible" directly above a warning naming an unclaimed one.
func TestLookalikeGuardOnAPlainPlanReport(t *testing.T) {
	const typeName = "aws_security_group"
	stripped := live(typeName, "sg-70c37520658184e7c", "stateless-e2e-main",
		map[string]string{"Name": "stateless-e2e-main"},
		map[string]string{"name": "stateless-e2e-main", "description": "estate fixture security group"})
	create := mustAddr(t, "aws_security_group.main")

	t.Run("before #1480: estate-scoped, and the guard is blind", func(t *testing.T) {
		// What Discover produced between 09d180f921 and #1480: one
		// config-driven row, server-side filtered, and an empty Unclaimed
		// because the stripped group carries no tofu-estate tag for the
		// filter to match.
		res := classifyFixture(t, discovery.Result{Report: discovery.Report{
			Scans: []discovery.TypeScan{{
				TypeName:  typeName,
				Filtering: discovery.FilterServerSide,
				Scope:     discovery.ScopeEstate,
				Declared:  1,
			}},
			Unbound: []addrs.AbsResourceInstance{create},
		}})

		if warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{create}); len(warnings) != 0 {
			t.Fatalf("the pre-#1480 report cannot produce a warning, so this fixture is wrong: %v", warnings)
		}
		u, ok := res.UnsweptOf(typeName)
		if !ok || u.Reason != UnsweptEstateScoped {
			t.Fatalf("want %s reported unswept/estate-scoped, got %+v (ok=%v)", typeName, u, ok)
		}
	})

	t.Run("after #1480: widened once, and the guard names the stripped group", func(t *testing.T) {
		// What Discover produces now: the same row, widened in place by
		// the one targeted list relistForLookalikes makes because
		// aws_security_group.main is unbound - so Scope reads ALL, and the
		// stripped group is in Unclaimed.
		res := classifyFixture(t, discovery.Result{Report: discovery.Report{
			Scans: []discovery.TypeScan{{
				TypeName:        typeName,
				Filtering:       discovery.FilterClientSide,
				Scope:           discovery.ScopeAll,
				Declared:        1,
				Unclaimed:       1,
				LookalikeRelist: true,
				RelistListed:    1,
			}},
			Unbound:   []addrs.AbsResourceInstance{create},
			Unclaimed: []discovery.UnclaimedResource{stripped},
		}})

		warnings := Lookalikes(Request{Estate: estateName}, res, []addrs.AbsResourceInstance{create})
		if len(warnings) != 1 {
			t.Fatalf("want exactly one lookalike warning, got %d: %v", len(warnings), warnings)
		}
		w := warnings[0]
		if w.Addr.String() != create.String() || w.LiveID != "sg-70c37520658184e7c" {
			t.Errorf("warning is %s, want aws_security_group.main ~ sg-70c37520658184e7c", w)
		}
		if len(w.Matched) != 1 || w.Matched[0].Attr != "name" {
			t.Errorf("warning carries matched arguments %v, want the name match that confirms it", w.Matched)
		}

		// The coverage sentence must not contradict the warning. A second
		// row appended beside the estate-scoped one - rather than the
		// widening in place discovery does - would print both.
		if u, ok := res.UnsweptOf(typeName); ok {
			t.Errorf("%s is reported unswept (%s: %s) directly above a warning naming an unclaimed resource of it", typeName, u.Reason, u.Detail)
		}
		found := false
		for _, s := range res.Swept {
			if s == typeName {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is not in Result.Swept, so the report says nothing about what was looked at: %v", typeName, res.Swept)
		}
	})
}
