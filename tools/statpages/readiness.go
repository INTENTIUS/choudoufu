// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statpages

import (
	"fmt"
	"strings"
)

// readinessData is the summary tools/readiness-gen -render writes from
// live/readiness.json: the cross tab, its provenance stamp, and every type
// with its one-line reason.
type readinessData struct {
	Stamp    string   `json:"stamp"`
	Statuses []string `json:"statuses"`
	Tiers    []struct {
		Tier   string `json:"tier"`
		Counts []int  `json:"counts"`
		Total  int    `json:"total"`
	} `json:"tiers"`
	ColumnTotals []int           `json:"column_totals"`
	Total        int             `json:"total"`
	Types        []readinessType `json:"types"`
	Paths        []readinessPath `json:"paths"`
	Kubernetes   struct {
		Provider string          `json:"provider"`
		Total    int             `json:"total"`
		Awaiting int             `json:"awaiting_ruling"`
		Types    []readinessType `json:"types"`
	} `json:"kubernetes"`
	Azure  readinessAzure `json:"azure"`
	Google struct {
		Provider string   `json:"provider"`
		Statuses []string `json:"statuses"`
		Tiers    []struct {
			Tier   string `json:"tier"`
			Counts []int  `json:"counts"`
			Total  int    `json:"total"`
		} `json:"tiers"`
		ColumnTotals []int           `json:"column_totals"`
		Total        int             `json:"total"`
		Paths        []readinessPath `json:"paths"`
		Types        []readinessType `json:"types"`
	} `json:"google"`
}

// readinessPath is one row of a path table; tools/readiness-gen's paths.go
// is where the paths are named and read from the tier facts.
type readinessPath struct {
	Path  string `json:"path"`
	Tier  string `json:"tier"`
	Count int    `json:"count"`
	Means string `json:"means"`
}

// readinessAzure is the Azure section (GitHub issue #2119).
type readinessAzure struct {
	Statuses []string               `json:"statuses"`
	Azurerm  readinessAzureProvider `json:"azurerm"`
	Azapi    readinessAzureProvider `json:"azapi"`
	Fence    []struct {
		Tier   string `json:"tier"`
		Counts []int  `json:"counts"`
		Fence  string `json:"fence"`
	} `json:"fence"`
	Scopes []struct {
		Scope string `json:"scope"`
		Fence string `json:"fence"`
	} `json:"scopes"`
	Types []readinessType `json:"types"`
}

type readinessAzureProvider struct {
	Provider string `json:"provider"`
	Tiers    []struct {
		Tier   string `json:"tier"`
		Counts []int  `json:"counts"`
		Total  int    `json:"total"`
	} `json:"tiers"`
	ColumnTotals     []int `json:"column_totals"`
	Total            int   `json:"total"`
	VersionDependent int   `json:"version_dependent"`
}

type readinessType struct {
	Type   string `json:"type"`
	Tier   string `json:"tier"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func readinessSpans(root string) (map[string]string, error) {
	var d readinessData
	if err := readJSON(root, ReadinessSiteRel, &d); err != nil {
		return nil, err
	}
	var tiers strings.Builder
	tiers.WriteString("| Tier")
	for _, s := range d.Statuses {
		fmt.Fprintf(&tiers, " | %s", s)
	}
	tiers.WriteString(" | Total |\n|---")
	for range d.Statuses {
		tiers.WriteString("|---")
	}
	tiers.WriteString("|---|\n")
	for _, t := range d.Tiers {
		fmt.Fprintf(&tiers, "| %s", t.Tier)
		for _, n := range t.Counts {
			fmt.Fprintf(&tiers, " | %d", n)
		}
		fmt.Fprintf(&tiers, " | %d |\n", t.Total)
	}
	tiers.WriteString("| Total")
	for _, n := range d.ColumnTotals {
		fmt.Fprintf(&tiers, " | %d", n)
	}
	fmt.Fprintf(&tiers, " | %d |\n\n%s\n", d.Total, d.Stamp)

	k8s := fmt.Sprintf("%s: %d admitted type(s), %d awaiting a tier ruling.\n\n", d.Kubernetes.Provider, d.Kubernetes.Total, d.Kubernetes.Awaiting) +
		typesTable(d.Kubernetes.Types)

	var gcp strings.Builder
	fmt.Fprintf(&gcp, "%s: %d resource types.\n\n| Tier", d.Google.Provider, d.Google.Total)
	for _, s := range d.Google.Statuses {
		fmt.Fprintf(&gcp, " | %s", s)
	}
	gcp.WriteString(" | Total |\n|---")
	for range d.Google.Statuses {
		gcp.WriteString("|---")
	}
	gcp.WriteString("|---|\n")
	for _, t := range d.Google.Tiers {
		fmt.Fprintf(&gcp, "| %s", t.Tier)
		for _, n := range t.Counts {
			fmt.Fprintf(&gcp, " | %d", n)
		}
		fmt.Fprintf(&gcp, " | %d |\n", t.Total)
	}
	gcp.WriteString("| Total")
	for _, n := range d.Google.ColumnTotals {
		fmt.Fprintf(&gcp, " | %d", n)
	}
	fmt.Fprintf(&gcp, " | %d |\n\n", d.Google.Total)
	gcp.WriteString(typesTable(d.Google.Types))

	return map[string]string{
		"tiers":     tiers.String(),
		"types":     typesTable(d.Types),
		"k8s-types": k8s,
		"azure":     azureReadiness(d.Azure),
		"gcp-types": gcp.String(),
		"paths":     pathsTable(d.Paths),
		"gcp-paths": pathsTable(d.Google.Paths),
	}, nil
}

// azureReadiness is the Azure section's span: each provider's cross tab,
// the fence each azurerm tier gets by where its types live, and the azurerm
// types short of in-contract.
func azureReadiness(a readinessAzure) string {
	var s strings.Builder
	crossTab := func(p readinessAzureProvider, unit string) {
		fmt.Fprintf(&s, "%s: %d %s.\n\n| Tier", p.Provider, p.Total, unit)
		for _, st := range a.Statuses {
			fmt.Fprintf(&s, " | %s", st)
		}
		s.WriteString(" | Total |\n|---")
		for range a.Statuses {
			s.WriteString("|---")
		}
		s.WriteString("|---|\n")
		for _, t := range p.Tiers {
			fmt.Fprintf(&s, "| %s", t.Tier)
			for _, n := range t.Counts {
				fmt.Fprintf(&s, " | %d", n)
			}
			fmt.Fprintf(&s, " | %d |\n", t.Total)
		}
		s.WriteString("| Total")
		for _, n := range p.ColumnTotals {
			fmt.Fprintf(&s, " | %d", n)
		}
		fmt.Fprintf(&s, " | %d |\n\n", p.Total)
	}
	crossTab(a.Azurerm, "resource types")
	crossTab(a.Azapi, "ARM resource types declared through azapi_resource")
	fmt.Fprintf(&s, "Each azapi ARM type is tiered by its latest API version; %d change taggability between versions, and the stamp reads the version a block names.\n\n", a.Azapi.VersionDependent)

	s.WriteString("The fence for azurerm, by tier and by where the type lives:\n\n| Tier")
	for _, sc := range a.Scopes {
		fmt.Fprintf(&s, " | %s", sc.Scope)
	}
	s.WriteString(" | What Policy adds to the group RBAC |\n|---")
	for range a.Scopes {
		s.WriteString("|---")
	}
	s.WriteString("|---|\n")
	for _, f := range a.Fence {
		fmt.Fprintf(&s, "| %s", f.Tier)
		for _, n := range f.Counts {
			fmt.Fprintf(&s, " | %d", n)
		}
		fmt.Fprintf(&s, " | %s |\n", cell(f.Fence))
	}
	s.WriteString("\n| Scope | What the estate's group RBAC reaches |\n|---|---|\n")
	for _, sc := range a.Scopes {
		fmt.Fprintf(&s, "| %s | %s |\n", sc.Scope, cell(sc.Fence))
	}
	s.WriteString("\nThe azurerm types short of in-contract:\n\n")
	s.WriteString(typesTable(a.Types))
	return s.String()
}

// pathsTable is how each type comes back: one row per path and tier, with
// the path's meaning on its first row.
func pathsTable(paths []readinessPath) string {
	var s strings.Builder
	s.WriteString("| How it comes back | Tier | Types | What it means |\n|---|---|---|---|\n")
	last := ""
	for _, p := range paths {
		tier, means := p.Tier, ""
		if tier == "" {
			tier = "-"
		}
		if p.Path != last {
			means = cell(p.Means)
		}
		last = p.Path
		fmt.Fprintf(&s, "| %s | %s | %d | %s |\n", p.Path, tier, p.Count, means)
	}
	return s.String()
}

// typesTable is one row per type. A reason is shared by hundreds of rows,
// so each distinct reason is printed once, numbered in order of first use,
// and the row carries its number. That keeps the page small enough for
// GitHub to render.
func typesTable(types []readinessType) string {
	num := map[string]int{}
	var reasons []string
	for _, t := range types {
		if t.Reason == "" {
			continue
		}
		if _, ok := num[t.Reason]; !ok {
			reasons = append(reasons, t.Reason)
			num[t.Reason] = len(reasons)
		}
	}
	var s strings.Builder
	if len(reasons) > 0 {
		s.WriteString("| Reason | Why the type is not in contract |\n|---|---|\n")
		for i, r := range reasons {
			fmt.Fprintf(&s, "| %d | %s |\n", i+1, cell(r))
		}
		s.WriteString("\n")
	}
	if len(reasons) == 0 {
		s.WriteString("| Type | Tier | Status |\n|---|---|---|\n")
	} else {
		s.WriteString("| Type | Tier | Status | Reason |\n|---|---|---|---|\n")
	}
	for _, t := range types {
		tier := t.Tier
		if tier == "" {
			tier = "unclassified"
		}
		if len(reasons) == 0 {
			fmt.Fprintf(&s, "| `%s` | %s | %s |\n", t.Type, tier, t.Status)
			continue
		}
		r := ""
		if t.Reason != "" {
			r = fmt.Sprint(num[t.Reason])
		}
		fmt.Fprintf(&s, "| `%s` | %s | %s | %s |\n", t.Type, tier, t.Status, r)
	}
	return s.String()
}
