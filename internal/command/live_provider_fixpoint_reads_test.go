// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestProviderConfigFixpointReadsEachSourceOncePerRequest is GitHub issue
// #1537's instrument, counting ReadDataSource calls PER PASS of
// [liveProviderDataReads] rather than in total.
//
// The fixture is live-target-provider-work, the one the issue measured on:
// two provider-configuration sources, data.aws_region.current (no
// arguments, readable on the first pass) and data.aws_eks_cluster.cluster
// (reads aws_eks_cluster.this, a managed value, so readable only after the
// fixpoint has read that cluster live). Passes are delimited by the
// fixpoint's own managed read: every ImportResourceState call starts a new
// pass, since [projection.ReadInstances] is the only thing between one
// analysis-and-read and the next.
//
// Before the fix the second pass read both sources, so the log split into
// [aws_region] then [aws_eks_cluster aws_region]. With the run's
// [dataread.ReadMemo] the second pass reads only the source the first
// could not: aws_region's request is byte-for-byte the one already sent,
// so its answer is reused.
//
// The control is the result map: aws_region must still be in it after the
// second pass, carried from the memo. A fix that simply stopped analyzing
// or reading on the second pass would pass the count and fail here.
func TestProviderConfigFixpointReadsEachSourceOncePerRequest(t *testing.T) {
	cfg := liveTestLoadConfig(t, filepath.Join("testdata", targetWorkFixture))

	resolveCloud := newTargetWorkCloud()
	resolutions, resolveDiags := liveResolve(t.Context(), cfg, resolveCloud, nil, nil, nil)
	if n := errorCount(resolveDiags); n != 0 {
		t.Fatalf("liveResolve refused with %d error(s): %v", n, renderDiags(resolveDiags))
	}

	cloud := newTargetWorkCloud()
	var (
		mu     sync.Mutex
		passes = [][]string{nil}
	)
	for _, m := range []*tofu.MockProvider{cloud.aws, cloud.kubernetes} {
		wrapPassLog(m, &mu, &passes)
	}

	results, _, diags := liveProviderDataReads(t.Context(), cfg, cloud, nil, resolutions, nil, 1, nil, nil)
	if diags.HasErrors() {
		t.Fatalf("the provider-configuration fixpoint raised an error: %v", renderDiags(diags))
	}

	got := make([]string, len(passes))
	for i, p := range passes {
		sorted := append([]string(nil), p...)
		sort.Strings(sorted)
		got[i] = "[" + strings.Join(sorted, " ") + "]"
	}
	want := []string{"[aws_region]", "[aws_eks_cluster]"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ReadDataSource calls per fixpoint pass:\n got %v\nwant %v\n"+
			"A pass after the first re-read a source an earlier pass already answered with the same request; "+
			"see dataread.ReadMemo and GitHub issue #1537.", got, want)
	}

	for _, addr := range []string{"data.aws_region.current", "data.aws_eks_cluster.cluster"} {
		if _, ok := results[addr]; !ok {
			t.Errorf("the fixpoint's final results lack %s; keys: %v", addr, sortedKeys(results))
		}
	}
}

// wrapPassLog splices a pass log into one fake provider's import and
// data-read hooks: an import opens a new pass (several imports in a row,
// one [projection.ReadInstances] call, open only one), and a data read is
// recorded in the pass that is open.
func wrapPassLog(p *tofu.MockProvider, mu *sync.Mutex, passes *[][]string) {
	importFn, readFn := p.ImportResourceStateFn, p.ReadDataSourceFn
	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		mu.Lock()
		if len(*passes) == 1 || len((*passes)[len(*passes)-1]) > 0 {
			*passes = append(*passes, nil)
		}
		mu.Unlock()
		return importFn(req)
	}
	p.ReadDataSourceFn = func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
		mu.Lock()
		(*passes)[len(*passes)-1] = append((*passes)[len(*passes)-1], req.TypeName)
		mu.Unlock()
		return readFn(req)
	}
}
