// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statpages

import (
	"fmt"
	"strings"
)

// forkData is the summary tools/forkdiff-gen -render writes from
// live/fork-surface.json.
type forkData struct {
	ForkPointShort      string         `json:"fork_point_short"`
	ForkPointSubject    string         `json:"fork_point_subject"`
	ForkPointURL        string         `json:"fork_point_url"`
	MeasuredAtHeadShort string         `json:"measured_at_head_short"`
	GeneratedAt         string         `json:"generated_at"`
	BaseOpenTofuVersion string         `json:"base_opentofu_version"`
	NamedRoots          []string       `json:"named_roots"`
	Counts              map[string]int `json:"counts"`
	NamedTotal          int            `json:"named_total"`
	OtherCount          int            `json:"other_count"`
	Total               int            `json:"total"`
	ModulePathOld       string         `json:"module_path_old"`
	ModulePathNew       string         `json:"module_path_new"`
	MechanicalModule    struct {
		ExcludedCount int `json:"excluded_count"`
	} `json:"mechanical_module_rename"`
}

func forkSpans(root string) (map[string]string, error) {
	var d forkData
	if err := readJSON(root, ForkSurfaceSiteRel, &d); err != nil {
		return nil, err
	}
	var s strings.Builder
	fmt.Fprintf(&s, "Counted at choudoufu commit `%s` on %s, against stock OpenTofu `%s` at fork point [`%s`](%s) (%q).\n\n",
		d.MeasuredAtHeadShort, d.GeneratedAt, d.BaseOpenTofuVersion, d.ForkPointShort, d.ForkPointURL, d.ForkPointSubject)
	fmt.Fprintf(&s, "%d files diverge. %d of them sit under the %d fork-owned roots below; the other %d are outside those roots, each named with its own reason in [`live/forkdiff_test.go`](forkdiff_test.go). A further %d files, not counted here, change only the Go module import path from `%s` to `%s`.\n\n",
		d.Total, d.NamedTotal, len(d.NamedRoots), d.OtherCount, d.MechanicalModule.ExcludedCount, d.ModulePathOld, d.ModulePathNew)
	s.WriteString("| Root | Files |\n|---|---|\n")
	for _, r := range d.NamedRoots {
		fmt.Fprintf(&s, "| `%s` | %d |\n", r, d.Counts[r])
	}
	fmt.Fprintf(&s, "| other (each justified) | %d |\n| Total | %d |\n", d.OtherCount, d.Total)
	return map[string]string{"fork-surface": s.String()}, nil
}
