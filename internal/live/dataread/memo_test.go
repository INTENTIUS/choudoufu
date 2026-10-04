// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestReadMemoReusesOnlyAnUnchangedRequest is GitHub issue #1537's rule at
// the package boundary, counted per pass: three calls to
// [ReadProviderConfigsMemo] sharing one [ReadMemo], standing in for three
// passes of internal/command's provider-configuration fixpoint.
//
//	pass 1, live id prod-cluster   1 read  (nothing answered yet)
//	pass 2, live id prod-cluster   0 reads (same request: the memo answers)
//	pass 3, live id other-cluster  1 read  (the managed value the source's
//	                                        argument reads changed, so the
//	                                        request did, and a fixpoint has
//	                                        to see the new answer)
//
// Each pass's result is also asserted, so a memo that returned nothing, or
// returned pass 1's answer to pass 3's different question, fails here even
// where the count happens to match.
//
// The last row is a nil memo over the same analysis as pass 3: it must
// read, which is [ReadProviderConfigs]' unchanged contract.
func TestReadMemoReusesOnlyAnUnchangedRequest(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "provider-config-demand"), nil)

	reads := 0
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			reads++
			name := req.Config.GetAttr("name")
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"name":    name,
				"zone_id": cty.StringVal("Z-" + name.AsString()),
			})}
		},
	}
	provs := &fakeProviders{provider: mock}
	liveWith := func(id string) map[string]cty.Value {
		return map[string]cty.Value{
			"module.child.aws_eks_cluster.this": cty.ObjectVal(map[string]cty.Value{
				"id": cty.StringVal(id),
			}),
		}
	}

	memo := NewReadMemo()
	for _, pass := range []struct {
		name      string
		clusterID string
		memo      *ReadMemo
		wantReads int
		wantZone  string
	}{
		{name: "pass 1", clusterID: "prod-cluster", memo: memo, wantReads: 1, wantZone: "Z-prod-cluster"},
		{name: "pass 2, unchanged request", clusterID: "prod-cluster", memo: memo, wantReads: 0, wantZone: "Z-prod-cluster"},
		{name: "pass 3, changed request", clusterID: "other-cluster", memo: memo, wantReads: 1, wantZone: "Z-other-cluster"},
		{name: "no memo", clusterID: "other-cluster", memo: nil, wantReads: 1, wantZone: "Z-other-cluster"},
	} {
		reads = 0
		analysis := AnalyzeProviderConfigs(context.Background(), cfg, Options{LiveManagedResults: liveWith(pass.clusterID)})
		results, diags := ReadProviderConfigsMemo(context.Background(), cfg, analysis, provs, pass.memo)
		if diags.HasErrors() {
			t.Fatalf("%s: read failed: %s", pass.name, diags.Err())
		}
		if reads != pass.wantReads {
			t.Errorf("%s: ReadDataSource called %d time(s), want %d", pass.name, reads, pass.wantReads)
		}
		got, ok := results["data.aws_zone.of_cluster"]
		if !ok {
			t.Errorf("%s: no result under data.aws_zone.of_cluster; keys: %v", pass.name, keysOf(results))
			continue
		}
		if zone := got.GetAttr("zone_id"); !zone.RawEquals(cty.StringVal(pass.wantZone)) {
			t.Errorf("%s: zone_id is %#v, want %q", pass.name, zone, pass.wantZone)
		}
	}
	if memo.Len() != 1 {
		t.Errorf("the memo holds %d answer(s), want 1: one source, its latest request replacing the earlier one", memo.Len())
	}
}

// TestReadMemoDoesNotRememberAFailedRead: a read that failed is not an
// answer, so the next pass asks again. A scoped failure is a warning, and
// treating it as settled would make a transient error a silent omission for
// the rest of the run.
func TestReadMemoDoesNotRememberAFailedRead(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "provider-config-demand"), nil)
	live := map[string]cty.Value{
		"module.child.aws_eks_cluster.this": cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("prod-cluster"),
		}),
	}

	reads := 0
	fail := true
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) (resp providers.ReadDataSourceResponse) {
			reads++
			if fail {
				resp.Diagnostics = resp.Diagnostics.Append(errQuota)
				return resp
			}
			name := req.Config.GetAttr("name")
			resp.State = cty.ObjectVal(map[string]cty.Value{
				"name":    name,
				"zone_id": cty.StringVal("Z-" + name.AsString()),
			})
			return resp
		},
	}
	provs := &fakeProviders{provider: mock}
	memo := NewReadMemo()

	analysis := AnalyzeProviderConfigs(context.Background(), cfg, Options{LiveManagedResults: live})
	results, _ := ReadProviderConfigsMemo(context.Background(), cfg, analysis, provs, memo)
	if _, ok := results["data.aws_zone.of_cluster"]; ok {
		t.Fatalf("a failed read produced a result")
	}
	if memo.Len() != 0 {
		t.Fatalf("the memo remembered a failed read: %d answer(s)", memo.Len())
	}

	fail = false
	results, diags := ReadProviderConfigsMemo(context.Background(), cfg, analysis, provs, memo)
	if diags.HasErrors() {
		t.Fatalf("second pass failed: %s", diags.Err())
	}
	if reads != 2 {
		t.Errorf("ReadDataSource called %d time(s) over both passes, want 2: the failed read must be retried", reads)
	}
	if _, ok := results["data.aws_zone.of_cluster"]; !ok {
		t.Errorf("the retried read produced no result")
	}
}
