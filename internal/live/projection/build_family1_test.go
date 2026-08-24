// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is GitHub issue #401 family 1's stub-seeding half:
// [builder.materializeFromRecord]'s record-first attempt at a type with no
// classic Importer (aws_acm_certificate_validation is the founding case)
// can only build [noimporter.SynthesizeStub] a value for an attribute NAME
// an identity Component resolved - never "id", the provider's own opaque
// state key, which no [identity.Component] ever names. A genuine
// ReadResource PriorState always carries the object's prior "id" (an
// ordinary refresh's own stub, or one ImportResourceState itself built);
// [recordFirstStubValues] closes that one structural gap by seeding it from
// the record's own [LocatedRecord.ImportID] - never inventing a value, only
// placing one the record already held under a name SynthesizeStub can use.

// TestRecordFirstStubValuesSeedsIDWhenComponentsAndImportIDBothPresent is
// the ordinary case, asserted by value: both fields present, "id" merged
// in without disturbing the existing component.
func TestRecordFirstStubValuesSeedsIDWhenComponentsAndImportIDBothPresent(t *testing.T) {
	rec := LocatedRecord{
		ImportID:   "2026-08-24 22:42:29.573 +0000 UTC",
		Components: map[string]string{"certificate_arn": "arn:aws:acm:eu-west-1:000000000000:certificate/deadbeef"},
	}
	got := recordFirstStubValues(rec)
	want := map[string]string{
		"certificate_arn": "arn:aws:acm:eu-west-1:000000000000:certificate/deadbeef",
		"id":              "2026-08-24 22:42:29.573 +0000 UTC",
	}
	if len(got) != len(want) {
		t.Fatalf("recordFirstStubValues = %#v, want %#v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("recordFirstStubValues[%q] = %q, want %q", k, got[k], v)
		}
	}

	// A copy, not a mutation: the record's own Components map must be
	// untouched by this call, since it may be reused or cached elsewhere -
	// see recordFirstStubValues's own doc comment.
	if _, ok := rec.Components["id"]; ok {
		t.Error("recordFirstStubValues mutated rec.Components in place - it must return a copy")
	}
	if len(rec.Components) != 1 {
		t.Errorf("rec.Components = %#v after the call, want unchanged (len 1)", rec.Components)
	}
}

// TestRecordFirstStubValuesLeavesEmptyComponentsUntouched is the mutation/
// boundary check: today's actual record shape for most of this run's
// record-first population - ImportID set, Components empty - must come
// back exactly as it went in, proving the len(rec.Components)>0 gate is
// load-bearing and not decorative. This is the shape
// noimporter.SynthesizeStub's own len(values)==0 refusal still depends on:
// seeding "id" alone, with no named identity component to place alongside
// it, would not make SynthesizeStub build anything different (its own
// early return never even looks at "id" as a special case), but it would
// be values a caller not expecting them might treat as "there was
// something to synthesize from" - so the gate stays exactly where the
// design put it.
func TestRecordFirstStubValuesLeavesEmptyComponentsUntouched(t *testing.T) {
	rec := LocatedRecord{ImportID: "2026-08-24 22:42:29.573 +0000 UTC"}
	got := recordFirstStubValues(rec)
	if len(got) != 0 {
		t.Errorf("recordFirstStubValues = %#v, want empty - Components was empty, and this must not manufacture a values map where there was none", got)
	}
}

// TestRecordFirstStubValuesLeavesEmptyImportIDUntouched is the other half
// of the same boundary: a record-backed/composite-only shape that carries
// Components but no separate ImportID string at all (a genuine wire
// identity object, [LocatedRecordFrom]'s Composite() branch) has no "id"
// to seed and must come back unchanged too.
func TestRecordFirstStubValuesLeavesEmptyImportIDUntouched(t *testing.T) {
	rec := LocatedRecord{Components: map[string]string{"zone_id": "Z1", "name": "n", "type": "CNAME"}}
	got := recordFirstStubValues(rec)
	if len(got) != len(rec.Components) {
		t.Fatalf("recordFirstStubValues = %#v, want unchanged %#v", got, rec.Components)
	}
	for k, v := range rec.Components {
		if got[k] != v {
			t.Errorf("recordFirstStubValues[%q] = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["id"]; ok {
		t.Error("recordFirstStubValues added \"id\" with no ImportID to seed it from")
	}
}

// TestNoClassicImporterStubCarriesSeededIDAlongsideComponents is the
// plumbing proof at the layer materializeFromRecord's seeded values
// actually flow into: importAndRead/noimporter.SynthesizeStub. It mirrors
// TestNoClassicImporterSynthesizesAStubFromTheResolvedIdentity
// (noimporter_test.go) exactly, except identityValues is what
// recordFirstStubValues would now hand materialize() - certificate_arn AND
// id both present - and asserts PriorState carries BOTH by value, proving
// the seeded "id" reaches ReadResource's own PriorState rather than being
// dropped somewhere in between.
func TestNoClassicImporterStubCarriesSeededIDAlongsideComponents(t *testing.T) {
	const arn = "arn:aws:acm:eu-west-1:000000000000:certificate/deadbeef"
	const importID = "2026-08-24 22:42:29.573 +0000 UTC"

	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var resp providers.ImportResourceStateResponse
		resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("resource aws_acm_certificate_validation doesn't support import"))
		return resp
	}
	var priorStateSeen cty.Value
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		priorStateSeen = r.PriorState
		return providers.ReadResourceResponse{
			NewState: cty.ObjectVal(map[string]cty.Value{
				"certificate_arn":         cty.StringVal(arn),
				"validation_record_fqdns": cty.NullVal(cty.Set(cty.String)),
				"id":                      cty.StringVal(importID),
			}),
		}
	}

	rec := LocatedRecord{ImportID: importID, Components: map[string]string{"certificate_arn": arn}}
	identityValues := recordFirstStubValues(rec)

	target := providers.ImportTarget{ID: importID}
	obj, _, status, diags := importAndRead(t.Context(), p, certificateValidationSchema(), "aws_acm_certificate_validation", target, importID, identityValues, nil, nil)

	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if status != statusMaterialized {
		t.Fatalf("status = %v, want statusMaterialized - a stub carrying both certificate_arn and id must let the read through", status)
	}
	if obj == nil {
		t.Fatal("obj is nil despite statusMaterialized")
	}

	if got := priorStateSeen.GetAttr("certificate_arn").AsString(); got != arn {
		t.Errorf("PriorState.certificate_arn = %q, want %q", got, arn)
	}
	if priorStateSeen.GetAttr("id").IsNull() {
		t.Error("PriorState.id is null - the seeded id from the record never reached the stub, which is the exact structural gap this unit closes")
	} else if got := priorStateSeen.GetAttr("id").AsString(); got != importID {
		t.Errorf("PriorState.id = %q, want %q", got, importID)
	}
}
