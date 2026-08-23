// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// The gauntlet's corpus-autoscaling-complete crossing found this shape live:
// aws_autoscaling_group has a ratified DefaultTable row (identity is the
// "name" argument, resolved statically whenever a configuration states one),
// so it is not in [MarkerlessTypes] - but its schema carries no top-level
// tags map at all ([TestNamePrefixDefersToDiscovery]'s sibling types,
// aws_db_parameter_group and aws_iam_role, both have one). An instance named
// through name_prefix therefore has neither a static name nor anywhere to
// carry an ownership marker: before [RecordFallbackType] existed,
// internal/live/stamp escalated it to "Unmarked apply of a marker-only
// resource" - a hard refusal on a construct stock OpenTofu applies without
// complaint, HANDOFF's first table row.
//
// This asserts the CLASS every instance in the fixture resolves to, by
// value: the untaggable name_prefix instance must resolve
// [ClassRecordLocated] (the record rung, HANDOFF's safety rule - never a
// wrong marker, never an outright refusal) precisely because a record_store
// is declared, its sibling that states a literal name must stay
// [ClassConcrete] with the literal name as its import ID (proving the
// fallback does not swallow the ordinary path), and the same fixture with no
// record_store declared must go back to [ClassNeedsDiscovery] (proving the
// fallback is not silently unconditional - see
// [TestRecordFallbackRequiresARecordStore]).
func TestRecordFallbackClassifiesUntaggableNamePrefix(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "record-fallback-untaggable"), nil)
	schemas := map[string]providers.Schema{
		"aws_autoscaling_group": locatedSchema(map[string]*configschema.Attribute{
			"name": {Type: cty.String, Optional: true, Computed: true},
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	assertNoErrors(t, diags)

	prefixed := resolutionAt(t, result, "aws_autoscaling_group.prefixed")
	if prefixed.Class != ClassRecordLocated {
		t.Fatalf("aws_autoscaling_group.prefixed resolved %s, want %s: untaggable and named through name_prefix, with a record_store declared, must drop to the record rung rather than refuse", prefixed.Class, ClassRecordLocated)
	}
	if prefixed.ImportID != "" {
		t.Errorf("aws_autoscaling_group.prefixed carries ImportID %q; ClassRecordLocated's identity comes from the store, never from this package, and a non-empty value here would be a wrong identity nothing verified", prefixed.ImportID)
	}

	named := resolutionAt(t, result, "aws_autoscaling_group.named")
	if named.Class != ClassConcrete {
		t.Fatalf("aws_autoscaling_group.named resolved %s, want %s: it states a literal name and must resolve from configuration alone, never reaching the fallback", named.Class, ClassConcrete)
	}
	if named.ImportID != "web-static" {
		t.Errorf("aws_autoscaling_group.named resolved to import ID %q, want %q - the literal value its own name argument states", named.ImportID, "web-static")
	}
}

// TestRecordFallbackRequiresARecordStore is the other half of the by-value
// assertion above: the identical fixture's untaggable name_prefix instance,
// resolved with the SAME schemas but no record_store in the configuration,
// must stay [ClassNeedsDiscovery] rather than silently landing on the record
// rung. [resolver.recordFallback] gates on [resolver.recordStore] before it
// ever asks [RecordFallbackType], and this is what proves that gate is live
// rather than vestigial - a record_store is the operator's own migration
// step (HANDOFF's foundation), and this fallback must never invent one.
func TestRecordFallbackRequiresARecordStore(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "name-prefix-discovery"), nil)
	schemas := map[string]providers.Schema{
		"aws_db_parameter_group": locatedSchema(map[string]*configschema.Attribute{
			"name":   {Type: cty.String, Optional: true, Computed: true},
			"family": {Type: cty.String, Required: true},
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
	assertNoErrors(t, diags)

	prefixed := resolutionAt(t, result, "aws_db_parameter_group.prefixed")
	if prefixed.Class != ClassNeedsDiscovery {
		t.Fatalf("aws_db_parameter_group.prefixed resolved %s with no record_store declared, want %s: the fallback must never fire without an operator-declared store", prefixed.Class, ClassNeedsDiscovery)
	}
}

// TestRecordFallbackTypeAgreesWithMarkersTaggable is the schema-only half:
// [RecordFallbackType] must clear a type only when [markers.Taggable] itself
// says the type's schema has nowhere to carry a marker. This is the
// predicate [TestRecordFallbackClassifiesUntaggableNamePrefix] exercises
// through a full resolve; this asserts it directly, against both a clean
// untaggable schema and its taggable twin, so a regression that stopped
// checking taggability at all - not just one that got the schema shape
// wrong - is caught here rather than only downstream.
func TestRecordFallbackTypeAgreesWithMarkersTaggable(t *testing.T) {
	untaggable := locatedSchema(map[string]*configschema.Attribute{
		"name": {Type: cty.String, Optional: true, Computed: true},
	})
	if !RecordFallbackType("aws_autoscaling_group", map[string]providers.Schema{"aws_autoscaling_group": untaggable}) {
		t.Fatal("RecordFallbackType(untaggable ASG-shaped schema) = false, want true")
	}

	taggable := locatedSchema(map[string]*configschema.Attribute{
		"name": {Type: cty.String, Optional: true, Computed: true},
		"tags": {Type: cty.Map(cty.String), Optional: true},
	})
	if RecordFallbackType("aws_autoscaling_group", map[string]providers.Schema{"aws_autoscaling_group": taggable}) {
		t.Fatal("RecordFallbackType(taggable ASG-shaped schema) = true, want false: a settable top-level tags map means the ordinary marker-fallback path applies, not the record rung")
	}
}
