// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package livegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/scenarioestate"
	"github.com/intentius/choudoufu/internal/live/waves"
)

// BenchmarkConfigOnlyGraph is #2285's cost measurement of the
// configuration-only graph at about the three sizes #2275 ruled (79, 745
// and 4,001 resources). It ran on terralith-gen scales 1, 10 and 54 until
// choudoufu#2303 moved it to the scenario estate (ruling A on
// INTENTIUS/chant#4586: the terralith is for the scale ladder only): 2, 17
// and 93 copies of live/e2e/estate, 86, 731 and 3,999 resources. The exact
// count is recorded beside the number as the "resources" metric. Each iteration loads the configuration tree and builds and
// encodes the document, as live-graph -json does without -from-plan.
// Recorded, not gated: nothing here asserts a threshold.
//
//	go test ./internal/live/livegraph/ -run '^$' -bench BenchmarkConfigOnlyGraph -benchmem -benchtime 10x
func BenchmarkConfigOnlyGraph(b *testing.B) {
	root := repoRoot(b)
	for _, scale := range []int{2, 17, 93} {
		b.Run(fmt.Sprintf("scale=%d", scale), func(b *testing.B) {
			dir := filepath.Join(b.TempDir(), "estate")
			summary, err := scenarioestate.Render(scenarioestate.Options{RepoRoot: root, Scale: scale, Prefix: "lg", Out: dir})
			if err != nil {
				b.Fatalf("rendering the scenario estate at scale %d: %v", scale, err)
			}
			ctx := context.Background()
			var resources, nodes, edges int
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cfg, err := waves.LoadTree(ctx, dir)
				if err != nil {
					b.Fatal(err)
				}
				bt := Build(cfg, Options{Dir: dir, Estate: "bench", Markers: testMarkers})
				if _, err := json.Marshal(bt.Doc); err != nil {
					b.Fatal(err)
				}
				nodes, edges = len(bt.Doc.Nodes), len(bt.Doc.Edges)
				resources = 0
				cfg.DeepEach(func(c *configs.Config) { resources += len(c.Module.ManagedResources) })
			}
			b.StopTimer()
			b.ReportMetric(float64(summary.Resources), "resources")
			b.ReportMetric(float64(resources), "blocks")
			b.ReportMetric(float64(nodes), "nodes")
			b.ReportMetric(float64(edges), "edges")
		})
	}
}

func repoRoot(tb testing.TB) string {
	tb.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		tb.Fatal(err)
	}
	mod := strings.TrimSpace(string(out))
	if mod == "" || mod == os.DevNull {
		tb.Fatal("not inside the choudoufu module")
	}
	return filepath.Dir(mod)
}
