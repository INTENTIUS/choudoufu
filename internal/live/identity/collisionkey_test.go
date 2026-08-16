// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// objectOnlySchemas builds the one provider schema this file needs: a type
// whose identity is two client-named attributes, which is what makes
// [SynthesizeTypeIdentity] mark the entry [TypeIdentity.IdentityObjectOnly]
// and [classify] leave its import ID empty on purpose.
//
// It is built here rather than read from a provider so the test states its
// own premise. The shape is aws_autoscaling_schedule's, the type the corpus
// hit this on.
func objectOnlySchemas() map[string]providers.Schema {
	return map[string]providers.Schema{
		"aws_autoscaling_schedule": {
			Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"autoscaling_group_name": {Type: cty.String, Required: true},
					"scheduled_action_name":  {Type: cty.String, Required: true},
				},
			},
			IdentitySchema: &configschema.Object{
				Nesting: configschema.NestingSingle,
				Attributes: map[string]*configschema.Attribute{
					"autoscaling_group_name": {Type: cty.String, Required: true},
					"scheduled_action_name":  {Type: cty.String, Required: true},
				},
			},
		},
	}
}

// TestIdentityObjectResolutionsAreComparedByTheirAttributes is the
// regression: three schedules in one group, differing only in the action
// name, must resolve to three distinct identities. Before the collision key
// read [Resolution.IdentityValues], all three compared equal on an import ID
// that is empty by design, and a configuration whose resources are perfectly
// distinct was refused.
//
// The assertion is on the rendered identity attributes rather than on
// whether any diagnostic fired, so a future change that keeps the count of
// refusals the same while binding two blocks to one live object still fails
// here.
func TestIdentityObjectResolutionsAreComparedByTheirAttributes(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "identity-object-distinct"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: objectOnlySchemas()})

	want := map[string]string{
		`aws_autoscaling_schedule.this["morning"]`:                          "morning",
		`aws_autoscaling_schedule.this["night"]`:                            "night",
		`aws_autoscaling_schedule.this["go-offline-to-celebrate-new-year"]`: "go-offline-to-celebrate-new-year",
	}
	for addr, action := range want {
		res := resolutionAt(t, result, addr)
		if res.Class != ClassConcrete {
			t.Errorf("%s resolved %s, want CONCRETE", addr, res.Class)
			continue
		}
		if got := res.IdentityValues["scheduled_action_name"]; got != action {
			t.Errorf("%s scheduled_action_name is %q, want %q", addr, got, action)
		}
		if got := res.IdentityValues["autoscaling_group_name"]; got != "shared-group" {
			t.Errorf("%s autoscaling_group_name is %q, want %q", addr, got, "shared-group")
		}
		if res.ImportID != "" {
			t.Errorf("%s import ID is %q; an identity-object-only type has none, which is the whole reason the collision key cannot read it", addr, res.ImportID)
		}
	}

	for _, d := range diags {
		if d.Description().Summary != "Two resources with the same identity" {
			continue
		}
		if !strings.Contains(d.Description().Detail, "aws_autoscaling_schedule.this[") {
			continue
		}
		t.Errorf("three distinct schedules were reported as colliding: %s", d.Description().Detail)
	}
}

// TestIdentityObjectCollisionStillFires is the other direction, and it is the
// one a fix like this can quietly break: comparing a more precise key must
// not stop the check from firing on two blocks that really do name the same
// live object.
func TestIdentityObjectCollisionStillFires(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "identity-object-distinct"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: objectOnlySchemas()})

	if !hasDiag(diags, "Two resources with the same identity", "aws_autoscaling_schedule.duplicate_b") {
		t.Fatalf("duplicate_a and duplicate_b name the same group and action and were not refused:\n%s", renderDiags(diags))
	}
	// The refusal has to name the identity, and for a type with no import ID
	// that means the attributes - not an empty string that says nothing.
	if hasDiag(diags, "Two resources with the same identity", `the identity ""`) {
		t.Errorf("the collision was reported against an empty identity string:\n%s", renderDiags(diags))
	}
	if !hasDiag(diags, "Two resources with the same identity", "scheduled_action_name=same-action") {
		t.Errorf("the collision did not render the identity attributes:\n%s", renderDiags(diags))
	}
}
