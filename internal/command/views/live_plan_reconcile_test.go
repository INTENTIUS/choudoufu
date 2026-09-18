// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/terminal"
)

// renderReconcilePolicy renders one policy report's reconciliation section
// and hands back everything the human view wrote.
func renderReconcilePolicy(t *testing.T, rep StatelessPolicyReport) string {
	t.Helper()
	streams, done := terminal.StreamsForTesting(t)
	NewStatelessPlan(NewView(streams)).Policy(rep)
	return done(t).All()
}

// TestReconcileSectionCountsWhatThisRunWillDestroy is the rendered half of
// GitHub issue #1257. "N live resources will be destroyed" is a claim about
// the plan, so it must count the candidates this run will actually destroy
// and not the ones its -target / -exclude left out - while the roster
// itself stays complete, because what the account holds is the report the
// operator asked for by setting undeclared_untagged = "delete".
//
// The two arms fail differently for the two ways of getting it wrong:
// counting the roster fails "one withheld" on the headline, and dropping
// withheld candidates from the list fails it on the roster.
func TestReconcileSectionCountsWhatThisRunWillDestroy(t *testing.T) {
	report := func(withheld string) StatelessPolicyReport {
		return StatelessPolicyReport{Reconcile: StatelessReconcile{
			Ran:       true,
			Threshold: 10,
			Roster: []StatelessReconcileCandidate{
				{TypeName: "aws_security_group", LiveID: "sg-kept"},
				{TypeName: "aws_security_group", LiveID: "sg-gone", Withheld: withheld},
			},
		}}
	}

	t.Run("nothing withheld", func(t *testing.T) {
		got := renderReconcilePolicy(t, report(""))
		if !strings.Contains(got, "2 live resources will be destroyed") {
			t.Errorf("an untargeted run destroys both candidates; got:\n%s", got)
		}
		if strings.Contains(got, "not destroyed here") || strings.Contains(got, "Withheld:") {
			t.Errorf("nothing was withheld, so neither line belongs; got:\n%s", got)
		}
	})

	t.Run("one withheld", func(t *testing.T) {
		got := renderReconcilePolicy(t, report("this run's -target/-exclude leaves it out of the plan graph"))
		if !strings.Contains(got, "1 live resource will be destroyed") {
			t.Errorf("the headline counts the plan, not the account; got:\n%s", got)
		}
		if !strings.Contains(got, "sg-gone") || !strings.Contains(got, "sg-kept") {
			t.Errorf("the roster is never narrowed - both candidates should still be listed; got:\n%s", got)
		}
		if !strings.Contains(got, "not destroyed here") {
			t.Errorf("a withheld candidate should carry its reason beside it; got:\n%s", got)
		}
		if !strings.Contains(got, "Withheld: 1 of the 2 candidates") {
			t.Errorf("the section should say how much of the roster this run left alone; got:\n%s", got)
		}
	})
}
