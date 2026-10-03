// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// labelsFamily stands in for the fixture's own provider family with a
// different marker carrier: a top-level settable labels map, and a
// sentence of its own for a type with none. Everything else is AWS's, by
// embedding, so the fixture resolves exactly as it does under AWS.
type labelsFamily struct{ substrate.Substrate }

const surfaceFakeLabels markers.Surface = "fake-labels"

func (labelsFamily) Surfaces() []markers.Surface { return []markers.Surface{surfaceFakeLabels} }
func (labelsFamily) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if block == nil {
		return "", false
	}
	if a, ok := block.Attributes["labels"]; ok && a.Optional && a.Type.IsMapType() {
		return surfaceFakeLabels, true
	}
	return "", false
}
func (labelsFamily) NotACarrier(_ *configschema.Block, typeName string) string {
	return fmt.Sprintf("%s has no labels map, the fake family's carrier.", typeName)
}

// TestNodeStampUnmarkedApplyAsksTheFamily (GitHub issue #1742 item 6): the
// check-tier unmarked-apply refusal asks the block's provider family
// whether the type carries a marker ([substrate.SurfaceOf]) and, when it
// does not, why ([substrate.NotACarrier]). Before, it asked
// markers.Taggable and markers.NotAMarkerSurface, the AWS answers, so a
// family whose carrier is not a tags map was refused for a marker it can
// carry, and told about a tags map in AWS's words when it could not.
func TestNodeStampUnmarkedApplyAsksTheFamily(t *testing.T) {
	orig := substrate.All
	var all []substrate.Substrate
	for _, s := range orig {
		if s == substrate.AWS {
			s = labelsFamily{substrate.AWS}
		}
		all = append(all, s)
	}
	substrate.All = all
	t.Cleanup(func() { substrate.All = orig })

	resolve := func(t *testing.T, schemas map[string]providers.Schema) Report {
		t.Helper()
		return Dir(t.Context(), stampUnmarkedApplyRecordBackedFixture, Context{Schemas: schemas})
	}
	run := func(t *testing.T, schemas map[string]providers.Schema) string {
		t.Helper()
		report := resolve(t, schemas)
		res, rdiags := identity.ResolveWith(t.Context(), report.Load.Config, identity.Context{Schemas: schemas})
		if rdiags.HasErrors() || len(res.NeedsDiscovery()) != 1 {
			t.Fatalf("resolving: %v, %d needs-discovery", rdiags.Err(), len(res.NeedsDiscovery()))
		}
		diags := NodeStampUnmarkedApply(report.Load.Config, res, schemas, "stampgaps-950", nil, nil, false)
		var details []string
		for _, d := range diags {
			if d.Description().Summary == stamp.SummaryUnmarkedApply {
				details = append(details, d.Description().Detail)
			}
		}
		return strings.Join(details, "\n")
	}

	t.Run("the family's carrier is not a tags map", func(t *testing.T) {
		labelled := map[string]providers.Schema{"aws_vpc": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"cidr_block": {Type: cty.String, Optional: true},
			"labels":     {Type: cty.Map(cty.String), Optional: true},
		}}}}
		if got := run(t, labelled); got != "" {
			t.Errorf("a type carrying the family's own marker was refused as unmarked:\n%s", got)
		}
	})

	t.Run("no carrier, in the family's words", func(t *testing.T) {
		bare := map[string]providers.Schema{"aws_vpc": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"cidr_block": {Type: cty.String, Optional: true},
		}}}}
		got := run(t, bare)
		if !strings.Contains(got, "aws_vpc has no labels map, the fake family's carrier.") {
			t.Errorf("refusal detail does not carry the family's NotACarrier sentence:\n%s", got)
		}
	})
}
