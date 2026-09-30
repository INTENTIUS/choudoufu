// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestNodeStampUnmarkedApply_writableStoreExemptsARecordableType is GitHub
// issue #1637's rule at the check tier, ruled 2026-09-27: a writable record
// store exempts a block whose identity the apply can record, and nothing
// else. The unrecordable case is the guard on the exemption's own premise:
// an apply that cannot write the record leaves the object as unfindable as
// #950 says, so the refusal must still fire there with a writable store.
func TestNodeStampUnmarkedApply_writableStoreExemptsARecordableType(t *testing.T) {
	cfg, result := resolveStampUnmarkedApplyRecordBackedFixture(t)
	recordable := stampUnmarkedApplyRecordBackedSchemas()
	if !projection.ApplyRecordsIdentity("aws_vpc", recordable["aws_vpc"]) {
		t.Fatal("fixture drift: this schema's aws_vpc should be recordable by the apply")
	}

	t.Run("no writable store: refuses", func(t *testing.T) {
		diags := NodeStampUnmarkedApply(cfg, result, recordable, "stampgaps-950", nil, nil, false)
		if !hasSummary(diags, stamp.SummaryUnmarkedApply) {
			t.Fatalf("want %q; got: %v", stamp.SummaryUnmarkedApply, diags.Err())
		}
	})

	t.Run("writable store, recordable type: exempt", func(t *testing.T) {
		diags, recordOnly := NodeStampUnmarkedApplyRecordOnly(cfg, result, recordable, "stampgaps-950", nil, nil, true)
		if diags.HasErrors() {
			t.Fatalf("the refusal fired with a writable store on a type the apply records: %s", diags.Err())
		}
		// GitHub issue #1743: the exempted instance is handed on, so the
		// write-back fails loudly if it cannot record it.
		var want []string
		for _, r := range result.NeedsDiscovery() {
			want = append(want, r.Addr.String())
		}
		var got []string
		for _, a := range recordOnly {
			got = append(got, a.String())
		}
		if len(want) == 0 || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("record-only instances = %v, want every exempted needs-discovery instance %v", got, want)
		}
	})

	t.Run("no writable store: nothing handed on", func(t *testing.T) {
		if _, recordOnly := NodeStampUnmarkedApplyRecordOnly(cfg, result, recordable, "stampgaps-950", nil, nil, false); len(recordOnly) != 0 {
			t.Fatalf("nothing was exempted, but %v were reported as record-only", recordOnly)
		}
	})

	t.Run("writable store, unrecordable type: refuses", func(t *testing.T) {
		// No "id" and no ratified components the apply can compose: the
		// write-back has nothing to record.
		unrecordable := map[string]providers.Schema{"aws_vpc": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"cidr_block": {Type: cty.String, Optional: true},
		}}}}
		if projection.ApplyRecordsIdentity("aws_vpc", unrecordable["aws_vpc"]) {
			t.Fatal("fixture drift: this schema's aws_vpc should not be recordable")
		}
		report := Dir(t.Context(), stampUnmarkedApplyRecordBackedFixture, Context{Schemas: unrecordable})
		res, rdiags := identity.ResolveWith(t.Context(), report.Load.Config, identity.Context{Schemas: unrecordable})
		if rdiags.HasErrors() || len(res.NeedsDiscovery()) != 1 {
			t.Fatalf("resolving: %v, %d needs-discovery", rdiags.Err(), len(res.NeedsDiscovery()))
		}
		diags := NodeStampUnmarkedApply(report.Load.Config, res, unrecordable, "stampgaps-950", nil, nil, true)
		if !hasSummary(diags, stamp.SummaryUnmarkedApply) {
			t.Fatalf("want %q with a writable store on a type the apply cannot record; got: %v", stamp.SummaryUnmarkedApply, diags.Err())
		}
	})
}
