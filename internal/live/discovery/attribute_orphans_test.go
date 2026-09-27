// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

// TestMergeAttributesOrphansToTheirPass is GitHub issue #1657's discovery
// half: every orphan Merge hands back names the provider configuration
// whose pass found it, for one pass and for several.
func TestMergeAttributesOrphansToTheirPass(t *testing.T) {
	east := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}
	west := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws"), Alias: "west"}
	pass := func(p addrs.AbsProviderConfig, ids ...string) Pass {
		res := &Result{Estate: "prod"}
		for _, id := range ids {
			res.Orphans = append(res.Orphans, OwnedResource{TypeName: "aws_sqs_queue", ImportID: id, Normalized: "aws_sqs_queue.gone_" + id})
		}
		return Pass{Provider: p, Result: res}
	}

	for name, passes := range map[string][]Pass{
		"one pass":   {pass(west, "w1")},
		"two passes": {pass(east, "e1", "e2"), pass(west, "w1")},
	} {
		t.Run(name, func(t *testing.T) {
			want := map[string]addrs.AbsProviderConfig{}
			for _, p := range passes {
				for _, o := range p.Result.Orphans {
					want[o.ImportID] = p.Provider
				}
			}
			res, _, diags := Merge("prod", passes, false)
			if diags.HasErrors() {
				t.Fatal(diags.Err())
			}
			if len(res.Orphans) != len(want) {
				t.Fatalf("got %d orphans, want %d", len(res.Orphans), len(want))
			}
			for _, o := range res.Orphans {
				if o.Provider.String() != want[o.ImportID].String() {
					t.Errorf("%s: Provider = %q, want %q", o.ImportID, o.Provider, want[o.ImportID])
				}
			}
		})
	}
}
