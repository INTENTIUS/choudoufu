// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The residue roster (issue #49): live/LIMITATIONS.md's "Exclusion
// cohorts" section names every cohort live/residue.go computes or curates,
// with counts and one-sentence reasons, so that "not covered" is a named,
// printable set rather than an implied one (#40's closing commitment).
//
// Five spans, one per cohort, each rendered the same way SURVEY.md's own
// spans are: from committed data, byte-for-byte, with no provider and no
// network. Two cohorts (deprecated services, registry-laggard live
// services) and one roster (emulator-blocked) carry hand judgment residue.go
// documents in its own comments; this file only formats what residue.go
// already computed or curated, it does not re-derive any of it.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	residue "github.com/intentius/choudoufu/live"
)

// limitationsMDRel is the doc whose residue-roster spans this file renders,
// and whose other mechanical claims tools/survey-gen/limitations_test.go
// holds to the committed survey artifact and the admission table.
const limitationsMDRel = "live/LIMITATIONS.md"

// The five residue-roster spans, one per exclusion cohort. Each lives in
// live/LIMITATIONS.md between a `<!-- survey-gen:begin NAME -->` line and a
// `<!-- survey-gen:end NAME -->` line, the same marker convention
// SURVEY.md's spans use (see render.go's spanMarkers).
const (
	spanResidueDeprecated = "residue-deprecated"
	spanResidueCFNOnly    = "residue-cfn-only"
	spanResidueUnmapped   = "residue-unmapped"
	spanResidueLaggard    = "residue-laggard"
	spanResidueEmulator   = "residue-emulator"
)

// renderLimitationsMD rewrites live/LIMITATIONS.md's five residue-roster
// spans in place, from live/residue.go's accessors (which in turn read the
// committed live/mapping.json and live/registry.json).
func renderLimitationsMD(root string) error {
	mdPath := filepath.Join(root, limitationsMDRel)
	md, err := os.ReadFile(mdPath) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return err
	}

	out, err := renderResidueSpans(string(md))
	if err != nil {
		return err
	}
	if out == string(md) {
		fmt.Fprintf(os.Stderr, "survey-gen: %s's residue-roster spans are already current\n", limitationsMDRel)
		return nil
	}
	if err := os.WriteFile(mdPath, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "survey-gen: rewrote %s's residue-roster spans\n", limitationsMDRel)
	return nil
}

// renderResidueSpans returns live/LIMITATIONS.md with all five
// residue-roster spans replaced by their rendered bodies. The rest of the
// file passes through byte-for-byte.
func renderResidueSpans(md string) (string, error) {
	spans := []struct {
		name string
		body string
	}{
		{spanResidueDeprecated, renderResidueDeprecated()},
		{spanResidueCFNOnly, renderResidueCFNOnly()},
		{spanResidueUnmapped, renderResidueUnmapped()},
		{spanResidueLaggard, renderResidueLaggard()},
		{spanResidueEmulator, renderResidueEmulator()},
	}
	var err error
	for _, s := range spans {
		md, err = replaceSpan(limitationsMDRel, md, s.name, s.body)
		if err != nil {
			return "", err
		}
	}
	return md, nil
}

// renderResidueDeprecated renders the deprecated/EOL services cohort: one
// row per live/residue.go DeprecatedServices entry, its registry-side
// count computed against live/registry.json, and the total.
func renderResidueDeprecated() string {
	var b strings.Builder
	b.WriteString("| Service | TF prefix | CFN registry types | Reason |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, d := range residue.DeprecatedServices {
		fmt.Fprintf(&b, "| %s | `%s` | %d | %s |\n", d.Service, d.TFPrefix, residue.DeprecatedCount(d), d.Reason)
	}
	fmt.Fprintf(&b, "\n**Total.** %d CloudFormation Registry types across %d services.\n",
		residue.DeprecatedTotal(), len(residue.DeprecatedServices))
	return b.String()
}

// renderResidueCFNOnly renders the CFN-only constructs cohort.
func renderResidueCFNOnly() string {
	var b strings.Builder
	b.WriteString("| CFN type | Reason |\n")
	b.WriteString("|---|---|\n")
	for _, c := range residue.CFNOnlyConstructs {
		fmt.Fprintf(&b, "| `%s` | %s |\n", c.Type, c.Reason)
	}
	fmt.Fprintf(&b, "\n**Total.** %d constructs, none counted against coverage: no Terraform configuration can name a CloudFormation-only type, so none can ever be refused either.\n",
		len(residue.CFNOnlyConstructs))
	return b.String()
}

// renderResidueUnmapped renders the unmapped-TF-types cohort: every
// distinct note among live/mapping.json's via:"none" rows, with its count,
// most common first.
func renderResidueUnmapped() string {
	groups, total := residue.UnmappedGroups()
	var b strings.Builder
	b.WriteString("| Count | Note |\n")
	b.WriteString("|---|---|\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "| %d | %s |\n", g.Count, g.Note)
	}
	fmt.Fprintf(&b, "\n**Total.** %d Terraform AWS resource types with no CloudFormation Registry counterpart. Each row's own note is in `live/mapping.json`.\n", total)
	return b.String()
}

// renderResidueLaggard renders the registry-laggard cohort: every TF type
// mapped to a CFN type whose Registry entry ships no working handler,
// excluding the deprecated-service overlap (residue.RegistryLaggardTypes
// already excludes it).
func renderResidueLaggard() string {
	types := residue.RegistryLaggardTypes()
	var b strings.Builder
	b.WriteString("| TF type | CFN type |\n")
	b.WriteString("|---|---|\n")
	for _, l := range types {
		fmt.Fprintf(&b, "| `%s` | `%s` |\n", l.TFType, l.CFNType)
	}
	fmt.Fprintf(&b, "\n**Total.** %d types, covered only where the provider's own identity schema reaches (the union `live/survey-full.json` measures). "+
		"A successor CFN type sometimes exists with working handlers - `AWS::Elasticsearch::Domain` above has no handlers, but its successor "+
		"`AWS::OpenSearchService::Domain` does; `live/mapping.json` does not yet link `aws_opensearch_domain` to it.\n", len(types))
	return b.String()
}

// renderResidueEmulator renders the emulator-blocked cohort (issue #26).
func renderResidueEmulator() string {
	sorted := append([]residue.EmulatorBlockedType(nil), residue.EmulatorBlocked...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Type < sorted[j].Type })

	var b strings.Builder
	b.WriteString("| Type | Admitted today | Reason |\n")
	b.WriteString("|---|---|---|\n")
	for _, e := range sorted {
		admitted := "no"
		if e.Admitted {
			admitted = "yes (standing e2e residue)"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", e.Type, admitted, e.Reason)
	}
	fmt.Fprintf(&b, "\n**Total.** %d types.\n", len(residue.EmulatorBlocked))
	return b.String()
}
