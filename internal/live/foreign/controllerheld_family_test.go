// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// fakeHoldingFamily is a third family that recognises one tag key no real
// family knows. It carries no surface, so it changes no surface question;
// everything else is AWS's, by embedding.
type fakeHoldingFamily struct {
	substrate.Substrate
}

const fakeHoldTag = "fake.example/held-by"

func (fakeHoldingFamily) Name() string                { return "fakehold" }
func (fakeHoldingFamily) Surfaces() []markers.Surface { return nil }
func (fakeHoldingFamily) ControllerHeld(ev substrate.HoldEvidence) (substrate.Hold, bool) {
	if v, ok := ev.Tags[fakeHoldTag]; ok {
		return substrate.Hold{Controller: "FakeCtl", HeldBy: "fake controller " + v}, true
	}
	return substrate.Hold{}, false
}

// TestRenameAsksTheFamilyForControllerHeld (GitHub issue #1711): the
// rename pass's controller-held skip asks substrate.ControllerHeld, not
// markers.ControllerHeld, so an orphan only a third family recognises as
// held is still never offered a rename. Without the fake family's tag the
// same orphan is TestRenameCandidate's one-to-one pairing.
func TestRenameAsksTheFamilyForControllerHeld(t *testing.T) {
	orig := substrate.All
	substrate.All = append(append([]substrate.Substrate(nil), orig...), fakeHoldingFamily{Substrate: substrate.AWS})
	t.Cleanup(func() { substrate.All = orig })

	o := orphan("aws_subnet", "subnet-xyz", "private-a", "aws_subnet.this:a")
	o.Tags = map[string]string{fakeHoldTag: "ns/thing"}
	res := classifyFixture(t, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{o}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_subnet", 1)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)}}})

	if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
		t.Errorf("an orphan the fake family holds was offered as a rename:\n%s", res)
	}
}
