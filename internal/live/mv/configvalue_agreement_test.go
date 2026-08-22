// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers/markerstest"
	"github.com/intentius/choudoufu/internal/plans/objchange"
)

// TestSyntheticConfigsMatchEveryOtherTagsOnlyWrite is the divergence guard for
// this package's copy of configValue, run against the same fixture
// internal/live/liveimport, internal/live/mv and internal/live/untag all use.
//
// The three packages each build the synthetic configuration a tags-only write
// pretends to have been given, from three character-identical copies of one
// function held in step by convention. That convention has failed before, in
// this exact set of three packages: [markerstest]'s package comment is about
// markers.Taggable growing a fifth clause that only one copy learned. GitHub
// issue #373 is the same hazard on the same three functions - a copy left
// behind refuses where the other two proceed, on one estate, depending only
// on which verb the operator ran.
func TestSyntheticConfigsMatchEveryOtherTagsOnlyWrite(t *testing.T) {
	block := markerstest.TagsOnlyConfigBlock()
	live := markerstest.TagsOnlyConfigObject()

	candidates := syntheticConfigs(block, live)
	if len(candidates) != 2 {
		t.Fatalf("syntheticConfigs returned %d candidates, want 2: this fixture has optional+computed attributes, so the two claims must differ", len(candidates))
	}
	least, most := candidates[0], candidates[1]

	// The claim that asserts least: every Computed attribute null.
	for _, name := range markerstest.TagsOnlyConfigNulled() {
		if got := least.GetAttr(name); !got.IsNull() {
			t.Errorf("least-claim %s = %#v, want null: the provider may compute it, so a configuration that only sets tags says nothing about it", name, got)
		}
	}
	// The claim that asserts most: only the never-settable ones null. This is
	// the pre-#373 rule, still reachable as the fallback, so it is pinned
	// here rather than left to git.
	for _, name := range markerstest.TagsOnlyConfigProviderOnly() {
		if got := most.GetAttr(name); !got.IsNull() {
			t.Errorf("most-claim %s = %#v, want null: a configuration cannot set it at all", name, got)
		}
	}
	for _, name := range markerstest.TagsOnlyConfigOptionalComputed() {
		if got, want := most.GetAttr(name), live.GetAttr(name); !got.RawEquals(want) {
			t.Errorf("most-claim %s = %#v, want %#v carried across", name, got, want)
		}
	}

	// Under either claim, the arguments only a configuration can supply are
	// carried: nulling one would propose removing it, since objchange takes a
	// non-computed attribute's config value verbatim.
	for i, cfg := range candidates {
		for _, name := range markerstest.TagsOnlyConfigCarried() {
			if got, want := cfg.GetAttr(name), live.GetAttr(name); !got.RawEquals(want) {
				t.Errorf("candidate %d: %s = %#v, want %#v carried across", i, name, got, want)
			}
		}

		// The nested-typed optional+computed attribute is the one exception,
		// and it is about objchange rather than about providers: a null config
		// for such an attribute whose prior holds any non-computed value reads
		// as a deliberate removal. Recursed into, not nulled.
		routing := cfg.GetAttr("routing")
		if routing.IsNull() {
			t.Fatalf("candidate %d: routing is null, want the block recursed into - objchange would read the null as dropping it", i)
		}
		if got, want := routing.GetAttr("mode"), live.GetAttr("routing").GetAttr("mode"); !got.RawEquals(want) {
			t.Errorf("candidate %d: routing.mode = %#v, want %#v", i, got, want)
		}

		// And the whole point, stated once per candidate: what the provider
		// reads back as configuration is all that moves. Against this same
		// object as prior, either proposal is the object itself - no attribute
		// moves, so nothing but the tags can be written.
		if proposed := objchange.ProposedNew(block, live, cfg); !proposed.RawEquals(live) {
			t.Errorf("candidate %d: the proposed new object is not the prior object:\n got: %#v\nwant: %#v", i, proposed, live)
		}
	}

	if got := least.GetAttr("routing").GetAttr("resolved"); !got.IsNull() {
		t.Errorf("least-claim routing.resolved = %#v, want null: a leaf inside a nested type is optional+computed like any other", got)
	}
}

// TestSyntheticConfigsCollapseWhenTheClaimsAgree: a type with no
// optional+computed attribute has one synthetic configuration, not two, so
// the fallback costs no second PlanResourceChange on the types it could never
// help. Most of what a stamp writes is not this shape - 673 of 682 taggable
// hashicorp/aws 6.59.0 types have at least one - but the collapse is what
// keeps the loop honest about being a fallback rather than a doubling.
func TestSyntheticConfigsCollapseWhenTheClaimsAgree(t *testing.T) {
	block := markerstest.FreeFormTagsBlock()
	live := markerstest.FreeFormTagsObject()

	if got := syntheticConfigs(block, live); len(got) != 1 {
		t.Fatalf("syntheticConfigs returned %d candidates for a type with no optional+computed attribute, want 1", len(got))
	}
}
