// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// reconcileScopeCloud is the fixture both arms below run against: three
// untagged strangers of one scoped, admitted, enumerable type. Two carry
// "targeted" in their import ID and one does not, so a scope can keep a
// strict, non-empty subset - which is what makes the middle arm able to
// fail differently from the last one.
func reconcileScopeCloud() *fakeCloud {
	cloud := newFakeCloud()
	cloud.listable("aws_security_group")
	cloud.obj("aws_security_group", "sg-targeted-a", nil)
	cloud.obj("aws_security_group", "sg-targeted-b", nil)
	cloud.obj("aws_security_group", "sg-excluded", nil)
	return cloud
}

// scopeKeepingReconcileTargeted keeps the two candidates whose synthetic
// address names an import ID containing "targeted". It is deliberately a
// scope that answers TRUE for something, which no scope
// [statelessTargetScope] builds ever does for a reconciliation candidate -
// see [ReconcileRequest.inScope]. A test that only ever used a real scope
// could not tell "narrows correctly" from "goes silent whenever narrowed",
// because both produce an empty roster.
func scopeKeepingReconcileTargeted() identity.Scope {
	return func(a addrs.ConfigResource) bool {
		return strings.Contains(a.Resource.Name, "targeted")
	}
}

// scopeKeepingNoReconcileCandidate is the shape a real -target run
// produces: the plan graph has no vertex for any minted address, so nothing
// on the roster is in it.
func scopeKeepingNoReconcileCandidate() identity.Scope {
	return func(addrs.ConfigResource) bool { return false }
}

// rosterIDs and withheldIDs read the fixture's own import IDs back off a
// result, so a failure message says which candidates moved rather than only
// how many.
func rosterIDs(cands []ReconcileCandidate) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.ImportID)
	}
	sort.Strings(out)
	return out
}

func withheldIDs(res *ReconcileResult) []string {
	var out []string
	for _, c := range res.Roster {
		if c.Withheld != "" {
			out = append(out, c.ImportID)
		}
	}
	sort.Strings(out)
	return out
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestReconcileHonoursTheTargetScope is GitHub issue #1257: the scoped
// account-reconciliation pass computed both of its outputs - the roster
// merged in as destroy proposals, and the threshold error that stops the
// run - over the whole account while the run had been narrowed to a handful
// of addresses.
//
// A threshold is the worst case of that shape, because it compares a count
// against a limit and both sides have to be about the same population. The
// ruling this pins: the threshold bounds how many live objects THIS RUN
// will destroy, so the count is [ReconcileResult.Proposable] - which is
// also, and by construction, exactly the set the caller merges in as
// destroy proposals. Neither the limit nor the check is narrowed; the
// population both of them are about is.
//
// The three arms distinguish the two ways to get this wrong, and the middle
// one is the reason this is a fix rather than a hole - narrowing a run must
// not disable a check protecting something the run DOES touch:
//
//	no narrowing at all  -> "keeps two" FAIL, "keeps none" FAIL
//	narrowed too far     -> "keeps two" FAIL, "keeps none" PASS
//
// Both were run against this test before it was trusted green.
func TestReconcileHonoursTheTargetScope(t *testing.T) {
	// A threshold of 1 sits strictly between the three-candidate account
	// and the two-candidate targeted share, so "over the threshold" is a
	// live question in both the unscoped and the keeps-two arm rather than
	// vacuously true in one of them.
	const threshold = 1

	run := func(t *testing.T, scope identity.Scope) *ReconcileResult {
		t.Helper()
		res, diags := Reconcile(context.Background(), ReconcileRequest{
			Estate:   estateName,
			Provider: reconcileScopeCloud(),
			Policy:   scopedPolicy("aws_security_group", threshold),
			Scope:    scope,
		})
		assertNoErrors(t, diags)
		if got := rosterIDs(res.Roster); len(got) != 3 {
			t.Fatalf("the roster is never narrowed - it is the report the operator asked for. want all three candidates, got %v", got)
		}
		return res
	}

	t.Run("no scope: every candidate is proposed and counted, exactly as before", func(t *testing.T) {
		res := run(t, nil)
		if got := rosterIDs(res.Proposable()); len(got) != 3 {
			t.Errorf("an untargeted run proposes destroying every candidate, got %v", got)
		}
		if got := withheldIDs(res); len(got) != 0 {
			t.Errorf("an untargeted run withholds nothing, got %v", got)
		}
		if !res.ThresholdExceeded {
			t.Errorf("three candidates over a threshold of %d must exceed it", threshold)
		}
	})

	t.Run("scope keeps two: those two are proposed, and still counted against the threshold", func(t *testing.T) {
		res := run(t, scopeKeepingReconcileTargeted())

		want := []string{"sg-targeted-a", "sg-targeted-b"}
		if got := rosterIDs(res.Proposable()); !sameIDs(got, want) {
			t.Errorf("want exactly %v proposed, got %v. Narrowing must not leave a destroy in place for a block the run does not hold, nor drop one it does.", want, got)
		}
		if got := withheldIDs(res); !sameIDs(got, []string{"sg-excluded"}) {
			t.Errorf("want exactly sg-excluded withheld, got %v", got)
		}
		if !res.ThresholdExceeded {
			t.Errorf("two candidates this run WILL destroy, over a threshold of %d, must still exceed it. "+
				"A run that stops being guarded the moment -target is passed has been broken, not narrowed.", threshold)
		}
	})

	t.Run("scope keeps none: nothing is proposed and the threshold cannot be exceeded", func(t *testing.T) {
		res := run(t, scopeKeepingNoReconcileCandidate())

		if got := rosterIDs(res.Proposable()); len(got) != 0 {
			t.Errorf("a run whose plan graph holds no candidate must propose destroying none, got %v", got)
		}
		if got := withheldIDs(res); len(got) != 3 {
			t.Errorf("every candidate should carry a withheld reason the report can print, got %v", got)
		}
		if res.ThresholdExceeded {
			t.Errorf("a run that will destroy nothing cannot be over a threshold of %d. "+
				"That refusal names a population the run was never going to touch, which is what #1257 filed.", threshold)
		}
		for _, c := range res.Roster {
			if !strings.Contains(c.Withheld, "-target") {
				t.Errorf("%s's withheld reason should say which flag left it out, got %q", c.ImportID, c.Withheld)
			}
		}
	})
}

// TestReconcileScopeIsAskedOfTheSyntheticAddress pins the bound
// [ReconcileRequest.inScope]'s doc comment states, because it is the reason
// the arm above has to invent a scope no real run produces: the address a
// candidate is asked about is [syntheticReconcileAddr]'s minted one, and a
// scope built from the configuration's plan graph has no vertex for it.
//
// Without this, a reader could believe the middle arm above describes
// something that happens in production. It does not, and the test says so
// rather than leaving the reader to work it out.
func TestReconcileScopeIsAskedOfTheSyntheticAddress(t *testing.T) {
	var asked []string
	res, diags := Reconcile(context.Background(), ReconcileRequest{
		Estate:   estateName,
		Provider: reconcileScopeCloud(),
		Policy:   scopedPolicy("aws_security_group", 10),
		Scope: func(a addrs.ConfigResource) bool {
			asked = append(asked, a.String())
			return false
		},
	})
	assertNoErrors(t, diags)
	if len(res.Roster) != 3 {
		t.Fatalf("want three candidates on the roster, got %d", len(res.Roster))
	}
	sort.Strings(asked)
	want := []string{
		"aws_security_group.reconcile_sg-excluded",
		"aws_security_group.reconcile_sg-targeted-a",
		"aws_security_group.reconcile_sg-targeted-b",
	}
	if !sameIDs(asked, want) {
		t.Errorf("the scope should be asked about each candidate's minted address; want %v, got %v", want, asked)
	}
}
