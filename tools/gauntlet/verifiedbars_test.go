// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestBarsSplitVerifiedFromCarried is #1558's rule (option 3, ruled
// 2026-10-02): a clear row measured on a pin that has since moved still
// counts toward Clear, and is counted as Carried rather than Verified, so
// the bars cannot present carried evidence as current. Each stale row
// differs from the current pins in exactly one field, so a predicate that
// forgets one of them leaves that row in Verified and fails here.
func TestBarsSplitVerifiedFromCarried(t *testing.T) {
	const (
		emu  = "floci@sha256:current"
		kind = "kindest/node@sha256:current"
		eng  = "1.13.0"
	)
	oracle := OracleVersions{Terraform: "1.16.1", Tofu: "1.13.0"}
	clearRun := func(img, sub string, o OracleVersions, engine string) EstateResult {
		r := EstateResult{Protocol: ProtocolGauntlet, Stages: map[string]string{}}
		for _, s := range HeadlineStages() {
			r.Stages[s.ID] = VerdictPass
		}
		oc := o
		r.LastRun = &LastRun{Commit: "c", Date: "2026-10-02", Oracle: &oc, UpstreamVersion: engine}
		if sub == SubstrateKind {
			r.LastRun.SubstrateImage = img
		} else {
			r.LastRun.Emulator = img
		}
		return r
	}
	rows := map[string]EstateResult{
		"current":      clearRun(emu, "", oracle, eng),
		"old-emulator": clearRun("floci@sha256:old", "", oracle, eng),
		"old-oracle":   clearRun(emu, "", OracleVersions{Terraform: "1.15.8", Tofu: "1.13.0"}, eng),
		"old-engine":   clearRun(emu, "", oracle, "1.12.0"),
		"no-oracle":    clearRun(emu, "", oracle, eng),
		"kind-current": clearRun(kind, SubstrateKind, oracle, eng),
		"kind-old":     clearRun("kindest/node@sha256:old", SubstrateKind, oracle, eng),
	}
	nr := rows["no-oracle"]
	nr.LastRun.Oracle = nil
	rows["no-oracle"] = nr

	m := &Manifest{}
	a := &Artifact{}
	for name, r := range rows {
		lane := "reference"
		if r.LastRun.SubstrateImage != "" {
			lane = LaneKubernetes
		}
		m.Estates = append(m.Estates, Estate{Name: name, Source: "s", Lane: lane, Set: SetCore, Reason: "r"})
		r.Name = name
		a.Estates = append(a.Estates, r)
	}
	a.Rebuild(m, nil, emu, oracle, ProviderVersions{}, eng, kind)

	for _, c := range []struct {
		key                      string
		sum                      SetSummary
		clear, verified, carried int
	}{
		{"core", a.Sets["core"], 5, 1, 4},
		{"kubernetes lane", a.Lanes[LaneKubernetes], 2, 1, 1},
	} {
		if c.sum.Clear != c.clear || c.sum.Verified != c.verified || c.sum.Carried != c.carried {
			t.Errorf("%s: clear %d, verified %d, carried %d; want %d, %d, %d",
				c.key, c.sum.Clear, c.sum.Verified, c.sum.Carried, c.clear, c.verified, c.carried)
		}
	}
}
