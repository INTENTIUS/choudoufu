// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestTaggableIsTheRunPredicate pins the generator's taggable signal to the
// predicate the run applies, on the clause the copy was missing.
//
// live/survey-full.json's signals.taggable is what row-gen's markerless rule
// reads, which is what identity.MarkerlessTypes carries, which is what lint
// admits from. So a generator that answers "taggable" where the run answers
// "not a marker surface" writes an admission into the table for a type
// stamping will refuse - and, through tools/estate-gen, writes estate tags
// into that type's tags argument in a cohort fixture.
//
// The copy had four of markers.Taggable's five clauses. The fifth arrived
// with issue #243: a tags map whose keys the provider documents as naming
// objects that must already exist is schema-identical to a free-form one
// and is not somewhere a marker can live.
//
// Measured against the real schemas rather than predicted:
//
//   - hashicorp/aws 6.59.0 - 1699 resource types, 847 with a settable
//     top-level tags map, and not one of the 847 carries a description at
//     all. The two predicates agree on every type, and regenerating
//     live/survey-full.json with the copy replaced changes nothing.
//   - hashicorp/google 7.44.0 - 1342 resource types, 26 with a top-level
//     tags attribute, every one of them described. markers.Taggable admits
//     none of them; the copy admitted 17.
//
// The descriptions below are quoted verbatim from that google 7.44.0
// schema, so this test's input is the provider's own text rather than a
// phrasing invented to match the regex.
func TestTaggableIsTheRunPredicate(t *testing.T) {
	tagsAttr := func(desc string) *configschema.Block {
		return &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"tags": {
				Type:        cty.Map(cty.String),
				Optional:    true,
				Description: desc,
			},
		}}
	}

	cases := []struct {
		name string
		desc string
		want bool
	}{{
		name: "free-form, undescribed - every taggable AWS type",
		desc: "",
		want: true,
	}, {
		name: "google_cloud_run_v2_service",
		desc: "A map of resource manager tags.\nResource manager tag keys and values have the same definition as resource manager tags.\nKeys must be in the format tagKeys/{tag_key_id}, and values are in the format tagValues/{tag_value_id}.",
		want: false,
	}, {
		name: "google_bigtable_instance",
		desc: "A map of Resource Manager Tags. Keys can be either the numeric tag key ID (tagKeys/123) or the namespaced name (project/tag-key). Values can be the numeric tag value ID (tagValues/456) or the namespaced value (project/tag-key/tag-value). The field is ignored when empty.",
		want: false,
	}, {
		// A described free-form map must stay admitted. "Documented,
		// therefore refused" would be the inverse of a useful signal, and
		// would empty the AWS table the day the provider starts describing
		// its own tags argument.
		name: "described but free-form",
		desc: "Key-value map of resource tags.",
		want: true,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := tagsAttr(tc.desc)

			// The external source: what the run itself would answer for
			// this schema. The generator's signal is not allowed a second
			// opinion about it.
			want := markers.Taggable(block)
			if want != tc.want {
				t.Fatalf("markers.Taggable = %v, and this case expects %v; the predicate under test is not the one this case was written for", want, tc.want)
			}
			if got := taggable(block); got != want {
				t.Errorf("survey-gen's taggable = %v, markers.Taggable = %v.\n"+
					"The generator's signal reaches lint admission through survey-full.json, row-gen's markerless rule and identity.MarkerlessTypes; a second answer here admits types the run refuses to stamp.",
					got, want)
			}
		})
	}
}
