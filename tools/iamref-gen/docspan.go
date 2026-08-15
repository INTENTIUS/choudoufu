// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/mdspan"
)

var markers = mdspan.For("iamref-gen")

const (
	markersMDRel    = "live/MARKERS.md"
	spanResourceTag = "resource-tag-services"
)

// renderResourceTagSpan writes the roster of services AWS's Service
// Authorization Reference names aws:ResourceTag on into live/MARKERS.md's
// estate-grant section (issue #142).
func renderResourceTagSpan(root string, rows []Row) error {
	path := filepath.Join(root, markersMDRel)
	doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", markersMDRel, err)
	}

	out, err := markers.Replace(markersMDRel, string(doc), spanResourceTag, resourceTagSpanBody(rows))
	if err != nil {
		return err
	}
	if out == string(doc) {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644) //nolint:gosec // a committed doc
}

// resourceTagSpanBody renders the roster. It is pure so the drift test can
// re-derive it from the shipped artifact and compare against the shipped
// document; nothing else keeps those two in step, since the span only moves
// when someone runs the generator, and a stale roster in a section about what
// IAM will actually evaluate is the exact failure the section warns about.
//
// One direction only, and the closing sentence says so in the document. The
// reference is authoritative about the condition keys it names and not about
// the ones it omits - lambda:GetFunction lists none at all while Lambda does
// support tag-based authorization - so this is the set where a marker-scoped
// grant is KNOWN to bite. The services absent from it are unmeasured, not
// disproven, and rendering them as a second column would be inventing a fact.
func resourceTagSpanBody(rows []Row) string {
	// Grouped by IAM prefix, not by CloudFormation service segment. The
	// action counts are the reference's, and the reference is indexed by IAM
	// prefix - so ResilienceHub and ResilienceHubV2, which both resolve to
	// "resiliencehub", are one row of 57 actions rather than two. Rendering
	// them separately shows the same 57 twice and inflates the denominator.
	type group struct {
		prefix   string
		services []string
		listing  int
		total    int
	}
	byPrefix := map[string]*group{}
	for _, r := range rows {
		if r.IAMPrefix == "" {
			continue
		}
		g := byPrefix[r.IAMPrefix]
		if g == nil {
			g = &group{prefix: r.IAMPrefix, listing: r.ActionsListingResourceTag, total: r.ActionsTotal}
			byPrefix[r.IAMPrefix] = g
		}
		g.services = append(g.services, r.Service)
	}

	var listed []*group
	for _, g := range byPrefix {
		sort.Strings(g.services)
		if g.listing > 0 {
			listed = append(listed, g)
		}
	}
	sort.Slice(listed, func(i, j int) bool {
		if listed[i].listing != listed[j].listing {
			return listed[i].listing > listed[j].listing
		}
		return listed[i].prefix < listed[j].prefix
	})

	var b strings.Builder
	b.WriteString("| Service | IAM prefix | Actions naming `aws:ResourceTag` |\n|---|---|---|\n")
	for _, g := range listed {
		fmt.Fprintf(&b, "| %s | `%s` | %d of %d |\n",
			strings.Join(g.services, ", "), g.prefix, g.listing, g.total)
	}
	fmt.Fprintf(&b, "\n%d of the %d IAM prefixes this estate's admitted types reach name the key on at "+
		"least one action. The remaining %d are **unmeasured, not disproven**: the reference does not "+
		"set out to enumerate every global condition key per action, and `lambda:GetFunction` lists none "+
		"at all while Lambda does support tag-based authorization. Read this as the set a marker-scoped "+
		"grant is known to bite on, never as its complement.\n",
		len(listed), len(byPrefix), len(byPrefix)-len(listed))
	return b.String()
}
