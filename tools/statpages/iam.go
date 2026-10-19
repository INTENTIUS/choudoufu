// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package statpages

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// iamData is live/iam-reference.json, as far as the page reads it.
type iamData struct {
	Source        string `json:"source"`
	IndexModified int64  `json:"index_modified"`
	Counts        struct {
		Services int `json:"services"`
		Resolved int `json:"resolved"`
	} `json:"counts"`
	Rows []struct {
		Service      string `json:"service"`
		IAMPrefix    string `json:"iam_prefix"`
		ActionsTotal int    `json:"actions_total"`
		Listing      *int   `json:"actions_listing_resource_tag"`
	} `json:"rows"`
}

type iamRow struct {
	service, prefix string
	listing, total  int
}

// iamSpans is the iamref shortcode as markdown: the services checked (one
// row per IAM prefix, the reference's own unit), split into those AWS names
// aws:ResourceTag for and those it says nothing about.
func iamSpans(root string) (map[string]string, error) {
	var d iamData
	if err := readJSON(root, IAMReferenceRel, &d); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var named, unnamed []iamRow
	for _, r := range d.Rows {
		if r.ActionsTotal <= 0 || seen[r.IAMPrefix] {
			continue
		}
		seen[r.IAMPrefix] = true
		row := iamRow{service: r.Service, prefix: r.IAMPrefix, total: r.ActionsTotal}
		if row.service == "" {
			row.service = r.IAMPrefix
		}
		if r.Listing != nil {
			row.listing = *r.Listing
			named = append(named, row)
		} else {
			unnamed = append(unnamed, row)
		}
	}
	sort.SliceStable(named, func(i, j int) bool {
		if named[i].listing != named[j].listing {
			return named[i].listing > named[j].listing
		}
		return named[i].prefix < named[j].prefix
	})
	sort.SliceStable(unnamed, func(i, j int) bool {
		if unnamed[i].total != unnamed[j].total {
			return unnamed[i].total > unnamed[j].total
		}
		return unnamed[i].prefix < unnamed[j].prefix
	})

	modified := "an unrecorded date"
	if d.IndexModified > 0 {
		modified = time.Unix(d.IndexModified, 0).UTC().Format("2006-01-02")
	}
	var s strings.Builder
	fmt.Fprintf(&s, "From AWS's Service Authorization Reference (<%s>), index last modified %s. The admission table reaches %d AWS services; %d resolve to exactly one IAM name, and %d IAM prefixes were checked for the condition key: %d name `aws:ResourceTag` on at least one action, %d name it on none.\n\n",
		d.Source, modified, d.Counts.Services, d.Counts.Resolved, len(named)+len(unnamed), len(named), len(unnamed))
	s.WriteString("### Services AWS names `aws:ResourceTag` for\n\n| Service | IAM prefix | Actions naming it | Of total |\n|---|---|---|---|\n")
	for _, r := range named {
		fmt.Fprintf(&s, "| %s | `%s` | %d | %d |\n", cell(r.service), r.prefix, r.listing, r.total)
	}
	s.WriteString("\n### Services the reference says nothing about\n\nUnmeasured, not disproven: the reference is authoritative about the keys it names and silent about the rest.\n\n| Service | IAM prefix | Actions |\n|---|---|---|\n")
	for _, r := range unnamed {
		fmt.Fprintf(&s, "| %s | `%s` | %d |\n", cell(r.service), r.prefix, r.total)
	}
	return map[string]string{"iam-reference": s.String()}, nil
}
