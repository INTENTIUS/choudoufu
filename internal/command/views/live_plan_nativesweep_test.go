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

// TestForeign_narrowedSweepSaysSo is asserted on the rendered section, not
// on a field: the whole content of the "Foreign resources" section is the
// difference between "we looked and there is nothing" and "we did not
// look", and rulings/20260830-stale-state-charter.md's CollectUnclaimed ruling
// makes the second answer the default for an ordinary plan. A run that did
// not ask has to say so, and has to say how to ask.
func TestForeign_narrowedSweepSaysSo(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	NewStatelessPlan(NewView(streams).SetRunningInAutomation(true)).Foreign(StatelessForeign{
		Estate:             "dev",
		NativeSweepSkipped: 987,
	})
	out := done(t).Stdout()

	for _, want := range []string{"987", "-adoption-only", "Every resource this estate owns was still swept for"} {
		if !strings.Contains(out, want) {
			t.Errorf("the Foreign section of a narrowed run does not mention %q:\n%s", want, out)
		}
	}
}

// TestForeign_unnarrowedSweepSaysNothingExtra is the other half: a run that
// DID ask the account-wide question must not print a caveat about a
// narrowing that never happened, which would be exactly as misleading in
// the other direction.
func TestForeign_unnarrowedSweepSaysNothingExtra(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	NewStatelessPlan(NewView(streams).SetRunningInAutomation(true)).Foreign(StatelessForeign{
		Estate: "dev",
		Swept:  []string{"aws_vpc"},
	})
	out := done(t).Stdout()

	if strings.Contains(out, "-adoption-only") {
		t.Errorf("a run that swept in full still told the reader to run -adoption-only for the account-wide question:\n%s", out)
	}
}
