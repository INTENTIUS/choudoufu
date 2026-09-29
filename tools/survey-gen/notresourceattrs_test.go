// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// TestNotResourceAttributes pins the identity-against-resource vocabulary
// comparison on the shape hashicorp/aws 6.59.0 ships for aws_osis_pipeline:
// the identity schema requires "name" and offers account_id and region, the
// resource schema has pipeline_name and region and no name or account_id.
func TestNotResourceAttributes(t *testing.T) {
	block := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"pipeline_name": {Type: cty.String, Required: true},
			"region":        {Type: cty.String, Optional: true, Computed: true},
			"id":            {Type: cty.String, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"vpc_options": {Nesting: configschema.NestingList},
		},
	}

	got := notResourceAttributes(block, []string{"name"}, []string{"account_id", "region"})
	if want := []string{"account_id", "name"}; !reflect.DeepEqual(got, want) {
		t.Errorf("notResourceAttributes = %v, want %v", got, want)
	}

	// A nested block counts as a resource attribute by that name, the same
	// rule identity.VerifyTable's hasAny applies.
	if got := notResourceAttributes(block, []string{"vpc_options"}, nil); got != nil {
		t.Errorf("a nested block by the identity attribute's name was reported missing: %v", got)
	}

	// Vocabularies that agree yield nil, so the artifact omits the field.
	if got := notResourceAttributes(block, []string{"pipeline_name"}, []string{"region"}); got != nil {
		t.Errorf("agreeing schemas produced %v, want nil", got)
	}

	// No resource schema at all: every identity attribute is a non-resource one.
	if got := notResourceAttributes(nil, []string{"arn"}, nil); !reflect.DeepEqual(got, []string{"arn"}) {
		t.Errorf("nil block: got %v, want [arn]", got)
	}
}
