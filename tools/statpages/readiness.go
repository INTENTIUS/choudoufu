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
	Kubernetes   struct {
		Provider string          `json:"provider"`
		Total    int             `json:"total"`
		Awaiting int             `json:"awaiting_ruling"`
		Types    []readinessType `json:"types"`
	} `json:"kubernetes"`
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
		Types        []readinessType `json:"types"`
	} `json:"google"`
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
		"gcp-types": gcp.String(),
	}, nil
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
