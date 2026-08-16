// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestStampGate_UnknownSchemaIsNotRefused is GitHub issue #230's repro. AWS's
// schema failed to load while random's succeeded (a routine shape - 33 of
// 250 corpus entries show one provider failing while another succeeds).
// aws_instance's ClassNeedsDiscovery classification comes from
// table_generated.go, not from AWS's schema being present, so this run
// cannot tell whether aws_instance is taggable - it is unknown, not refused.
//
// Before the fix, this fabricated a hard "Unmarked apply of a marker-only
// resource" error on aws_instance.web because the old gate
// (len(actx.Schemas) > 0) went true the moment random_id's schema loaded,
// and stamp.Stamp's SkipNoSchema path escalated to an error because
// mustStamp still read true for aws_instance.
func TestStampGate_UnknownSchemaIsNotRefused(t *testing.T) {
	schemas := map[string]providers.Schema{
		"random_id": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"byte_length": {Type: cty.Number, Required: true},
				"id":          {Type: cty.String, Computed: true},
				"hex":         {Type: cty.String, Computed: true},
			},
		}},
		// aws_instance's own schema is deliberately absent: AWS's schema
		// acquisition is the thing that failed in this shape.
	}

	report := Dir(t.Context(), filepath.Join("testdata", "stamp-schema-gate"), Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}
	if !report.Schemas {
		t.Fatal("report.Schemas is false; the repro needs at least one schema present (random_id's), matching issue #230's shape")
	}

	for _, f := range report.Findings {
		if f.Layer == LayerStamp && f.ID == stamp.SummaryUnmarkedApply {
			t.Fatalf("stamp fabricated a refusal on aws_instance.web with its schema unavailable: %v", f)
		}
	}
	for _, f := range report.Warnings {
		if f.Layer == LayerStamp && f.ID == stamp.SummaryUnmarkedApply {
			t.Fatalf("stamp fabricated a refusal (as a warning) on aws_instance.web with its schema unavailable: %v", f)
		}
	}
}

// TestStampGate_NoSchemasAtAllIsNotRefused is the zero-schema case the old
// gate (len(actx.Schemas) > 0) handled by never calling stamp.Stamp at all.
// The fix removes that outer gate in favor of the per-type one inside
// stampNeedsDiscovery, so this pins that the zero-schema case still produces
// no fabricated stamp refusal now that stamp.Stamp always runs when identity
// resolved.
func TestStampGate_NoSchemasAtAllIsNotRefused(t *testing.T) {
	report := Dir(t.Context(), filepath.Join("testdata", "stamp-schema-gate"), Context{})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}
	if report.Schemas {
		t.Fatal("report.Schemas is true with no schemas given")
	}

	for _, f := range report.Findings {
		if f.Layer == LayerStamp {
			t.Fatalf("stamp produced a finding with no schemas at all: %v", f)
		}
	}
}

// TestStampGate_GenuinelyUntaggableTypeStillRefuses is the mirror check the
// fix must not break: aws_cloudfront_cache_policy is ClassNeedsDiscovery
// (server-assigned) and its real provider schema has no tags/tags_all
// argument at all - there is nowhere to carry a marker, and no static
// identity either, so an unmarked apply of it genuinely creates a resource
// this configuration can never find again. With ITS OWN schema present, that
// must still be a hard refusal: gating per type must not silently swallow a
// real "untaggable" classification along with the "unknown" one issue #230
// is about.
func TestStampGate_GenuinelyUntaggableTypeStillRefuses(t *testing.T) {
	schemas := map[string]providers.Schema{
		"aws_cloudfront_cache_policy": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":      {Type: cty.String, Computed: true},
				"name":    {Type: cty.String, Required: true},
				"min_ttl": {Type: cty.Number, Optional: true},
				// Deliberately no "tags" or "tags_all": the real schema has
				// none, which is the whole reason this type refuses.
			},
		}},
	}

	report := Dir(t.Context(), filepath.Join("testdata", "stamp-untaggable-with-schema"), Context{Schemas: schemas})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	var found bool
	for _, f := range report.Findings {
		if f.Layer == LayerStamp && f.ID == stamp.SummaryUnmarkedApply {
			found = true
			if len(f.Sites) == 0 {
				t.Error("refusal has no sites")
			}
		}
	}
	if !found {
		t.Fatalf("aws_cloudfront_cache_policy, genuinely untaggable with its own schema present, was not refused: %v", findingIDs(report))
	}
}
