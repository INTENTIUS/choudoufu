// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
)

// reconcileCandidate mints one roster entry the way
// discovery.syntheticReconcileAddr does, which is all this side of GitHub
// issue #1257 needs: the command layer never re-derives an address, it only
// decides which candidates become destroy proposals.
func reconcileCandidate(importID, withheld string) discovery.ReconcileCandidate {
	return discovery.ReconcileCandidate{
		TypeName: "aws_security_group",
		ImportID: importID,
		Addr: addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_security_group",
			Name: "reconcile_" + importID,
		}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
		Withheld: withheld,
	}
}

// TestReconcileResolutionsHonourTheTargetScope is the command half of
// GitHub issue #1257, and the half that decides whether the threshold fix
// is a fix or a hole.
//
// [statelessPolicyReconcile] does two things with one roster: it raises a
// threshold error over a count, and it merges candidates in as synthetic
// resolutions the plan engine turns into destroy proposals. Narrowing one
// without the other is worse than narrowing neither - a run whose destroys
// no threshold ever counted. So both read
// [discovery.ReconcileResult.Proposable], and this pins the second.
//
// The two failure directions, run against this test before it was trusted:
//
//	reads Roster instead of Proposable -> "withheld" FAIL, "all withheld" FAIL
//	returns nothing whenever anything is withheld -> "withheld" FAIL, "all withheld" PASS
func TestReconcileResolutionsHonourTheTargetScope(t *testing.T) {
	addrsOf := func(rec *discovery.ReconcileResult) []string {
		extra, verified := reconcileResolutions(rec)
		var out []string
		for _, r := range extra {
			if !r.Undeclared {
				t.Errorf("%s must enter as an undeclared resolution, the same way a swept orphan does", r.Addr)
			}
			if !verified[r.Addr.String()] {
				t.Errorf("%s is proposed for destruction and must also be vouched for in the verified set", r.Addr)
			}
			out = append(out, r.Addr.String())
		}
		if len(verified) != len(extra) {
			t.Errorf("the verified set and the resolution list must name the same candidates; got %d verified against %d resolutions", len(verified), len(extra))
		}
		sort.Strings(out)
		return out
	}

	const withheld = "this run's -target/-exclude leaves it out of the plan graph"

	t.Run("nothing withheld: every candidate is proposed, exactly as before", func(t *testing.T) {
		got := addrsOf(&discovery.ReconcileResult{Roster: []discovery.ReconcileCandidate{
			reconcileCandidate("sg-a", ""),
			reconcileCandidate("sg-b", ""),
		}})
		want := []string{"aws_security_group.reconcile_sg-a", "aws_security_group.reconcile_sg-b"}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("want %v proposed on an untargeted run, got %v", want, got)
		}
	})

	t.Run("one withheld: the other is still proposed", func(t *testing.T) {
		got := addrsOf(&discovery.ReconcileResult{Roster: []discovery.ReconcileCandidate{
			reconcileCandidate("sg-kept", ""),
			reconcileCandidate("sg-gone", withheld),
		}})
		if len(got) != 1 || got[0] != "aws_security_group.reconcile_sg-kept" {
			t.Errorf("want exactly sg-kept proposed, got %v. A withheld candidate must not be destroyed, and a kept one must not stop being.", got)
		}
	})

	t.Run("all withheld: nothing is proposed", func(t *testing.T) {
		got := addrsOf(&discovery.ReconcileResult{Roster: []discovery.ReconcileCandidate{
			reconcileCandidate("sg-a", withheld),
			reconcileCandidate("sg-b", withheld),
		}})
		if len(got) != 0 {
			t.Errorf("a run whose plan graph holds no candidate must propose destroying none, got %v", got)
		}
	})
}
