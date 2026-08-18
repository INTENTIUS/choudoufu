// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestArityRefusesLengthOverAnUnexpandedProjection and
// TestArityRefusesKeysOverAnUnexpandedProjection are issue #193's
// reproduction, fixed: length()/keys() over an unexpanded managed
// resource's own projection must refuse cleanly at BOTH phases - offline
// classification (Analyze) and the value-returning read (Read) - never
// answer len(common)+1 or leak [unprojectedAttr]'s own name as a value.

func TestArityRefusesLengthOverAnUnexpandedProjection(t *testing.T) {
	a := analyzeFixture(t, "zz-audit-arity")
	src, ok := a.SourceFor(addrs.RootModule, dataAddr("test_subnet", "arity"))
	if !ok {
		t.Fatalf("data.test_subnet.arity was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("length(aws_instance.web) over an unexpanded block must refuse, not read len(common)+1")
	}
	if src.ReasonSummary != SummaryNotReadable {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryNotReadable)
	}
	if !strings.Contains(src.ReasonDetail, "length()") {
		t.Errorf("the refusal does not name length(): %s", src.ReasonDetail)
	}
	if strings.Contains(src.ReasonDetail, unprojectedAttr) {
		t.Errorf("the sentinel's own name leaked into the refusal: %s", src.ReasonDetail)
	}
}

func TestArityRefusesKeysOverAnUnexpandedProjection(t *testing.T) {
	a := analyzeFixture(t, "zz-audit-arity")
	src, ok := a.SourceFor(addrs.RootModule, dataAddr("test_subnet", "keyed"))
	if !ok {
		t.Fatalf("data.test_subnet.keyed was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("keys(aws_instance.web) over an unexpanded block must refuse, not leak the sentinel's own name")
	}
	if src.ReasonSummary != SummaryNotReadable {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryNotReadable)
	}
	if !strings.Contains(src.ReasonDetail, "keys()") {
		t.Errorf("the refusal does not name keys(): %s", src.ReasonDetail)
	}
	if strings.Contains(src.ReasonDetail, unprojectedAttr) {
		t.Errorf("the sentinel's own name leaked into the refusal: %s", src.ReasonDetail)
	}
}

// TestArityReadNeverReachesTheProviderWithAWrongOrLeakedValue is the read
// side of the reproduction: both sources are already ineligible per the
// tests above, so Read must refuse before ever asking the mock provider
// for anything - the old defect's own report was the provider actually
// being called with `[n-4 //unprojected-ami-instance_type-subnet_id]`.
func TestArityReadNeverReachesTheProviderWithAWrongOrLeakedValue(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "zz-audit-arity"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	reads := 0
	var sawIDs []string
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: projectionProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			reads++
			id := req.Config.GetAttr("id")
			if id.IsKnown() && !id.IsNull() {
				sawIDs = append(sawIDs, id.AsString())
			}
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"id":         id,
				"cidr_block": cty.StringVal("10.0.0.0/24"),
			})}
		},
	}

	_, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if !diags.HasErrors() {
		t.Fatalf("the read succeeded; length()/keys() over an unexpanded projection must refuse")
	}
	if reads != 0 {
		t.Errorf("the provider was called %d times; want 0 - both sources are ineligible", reads)
	}
	for _, id := range sawIDs {
		if strings.Contains(id, unprojectedAttr) || id == "n-4" {
			t.Errorf("a wrong or leaked value reached the provider: %q", id)
		}
	}
	if strings.Contains(diags.Err().Error(), unprojectedAttr) {
		t.Errorf("the sentinel's own name leaked into a diagnostic: %s", diags.Err())
	}
}

// TestArityGuardLeavesAnExpandedBlockAlone is the scope boundary the fix
// must not cross: a count-expanded block projects as a tuple and a
// for_each-expanded one as an object keyed by the block's own each.key
// strings, and length()/keys() over EITHER already answered correctly
// before this fix (real OpenTofu's own answer: the instance count, and -
// for for_each - those very key strings). The guard fires only on the
// literal "//unprojected" sentinel attribute [managedProjector.build]'s
// unexpanded case alone carries at the object's own top level, so both
// reads here must stay eligible and answer the provider with the real,
// unrefused values.
func TestArityGuardLeavesAnExpandedBlockAlone(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-projection-arity-expanded"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	for _, name := range []string{"count_arity", "foreach_arity"} {
		src, ok := analysis.SourceFor(addrs.RootModule, dataAddr("test_subnet", name))
		if !ok {
			t.Fatalf("data.test_subnet.%s was not classified at all", name)
		}
		if !src.Eligible {
			t.Fatalf("data.test_subnet.%s must stay eligible for an expanded block; refused: %s", name, src.ReasonDetail)
		}
	}

	var sawIDs []string
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: projectionProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			id := req.Config.GetAttr("id")
			sawIDs = append(sawIDs, id.AsString())
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"id":         id,
				"cidr_block": cty.StringVal("10.0.0.0/24"),
			})}
		},
	}

	if _, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock}); diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}

	want := map[string]bool{"n-3": false, "a-b": false}
	for _, id := range sawIDs {
		if _, known := want[id]; !known {
			t.Errorf("unexpected id %q reached the provider", id)
			continue
		}
		want[id] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("expected id %q never reached the provider; saw %v", id, sawIDs)
		}
	}
}
