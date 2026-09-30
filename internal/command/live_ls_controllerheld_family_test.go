// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// lsHoldingFamily is a third family that recognises one tag key no real
// family knows. It carries no surface, so it changes no surface question;
// everything else is AWS's, by embedding.
type lsHoldingFamily struct {
	substrate.Substrate
}

const lsFakeHoldTag = "fake.example/held-by"

func (lsHoldingFamily) Name() string                { return "fakehold" }
func (lsHoldingFamily) Surfaces() []markers.Surface { return nil }
func (lsHoldingFamily) ControllerHeld(ev substrate.HoldEvidence) (substrate.Hold, bool) {
	if v, ok := ev.Tags[lsFakeHoldTag]; ok {
		return substrate.Hold{Controller: "FakeCtl", HeldBy: "fake controller " + v}, true
	}
	return substrate.Hold{}, false
}

// TestLiveLs_heldColumnAsksTheFamily (GitHub issue #1711): live-ls's held
// column asks substrate.ControllerHeld, not markers.ControllerHeld, so an
// item only a third family recognises as held is named, in that family's
// own words.
func TestLiveLs_heldColumnAsksTheFamily(t *testing.T) {
	orig := substrate.All
	substrate.All = append(append([]substrate.Substrate(nil), orig...), lsHoldingFamily{Substrate: substrate.AWS})
	t.Cleanup(func() { substrate.All = orig })

	item := liveLsItemFromTags("arn:aws:s3:::fake-made-bucket", map[string]string{
		"tofu-estate":  "prod",
		"tofu-address": "aws_s3_bucket.assets",
		lsFakeHoldTag:  "ns/thing",
	}, "tagging")
	if want := "fake controller ns/thing"; item.HeldBy != want {
		t.Errorf("HeldBy = %q, want the fake family's %q", item.HeldBy, want)
	}
}
