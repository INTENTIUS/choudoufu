// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/foreign"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestLivePostCreateMarker_route53 is GitHub issue #2009's wiring: the
// lookalike warning's post-create flag comes from the embedded registry
// through the same question the apply asks, so aws_route53_zone and
// aws_route53_health_check carry it and a type that takes its tags in the
// create call does not.
func TestLivePostCreateMarker_route53(t *testing.T) {
	taggable := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}}}
	schemas := map[string]providers.Schema{
		"aws_route53_zone":         taggable,
		"aws_route53_health_check": taggable,
		"aws_vpc":                  taggable,
	}
	addr := func(typ string) addrs.AbsResourceInstance {
		return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typ, Name: "x"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	}

	postCreate := livePostCreateMarker(nil, schemas, markerFacts())
	for typ, want := range map[string]bool{
		"aws_route53_zone":         true,
		"aws_route53_health_check": true,
		"aws_vpc":                  false,
		"aws_not_in_schemas":       false,
	} {
		if got := postCreate(addr(typ)); got != want {
			t.Errorf("livePostCreateMarker(%s) = %t, want %t", typ, got, want)
		}
	}

	report := liveLookalikeReport([]foreign.Lookalike{
		{Addr: addr("aws_route53_zone"), TypeName: "aws_route53_zone"},
		{Addr: addr("aws_vpc"), TypeName: "aws_vpc"},
	}, postCreate)
	if !report[0].PostCreateMarker || report[1].PostCreateMarker {
		t.Errorf("liveLookalikeReport flags = %t, %t; want true, false", report[0].PostCreateMarker, report[1].PostCreateMarker)
	}
	if liveLookalikeReport([]foreign.Lookalike{{Addr: addr("aws_route53_zone")}}, nil)[0].PostCreateMarker {
		t.Error("a nil post-create answer flagged a lookalike")
	}
}
