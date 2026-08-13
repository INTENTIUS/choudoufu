// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The residue roster (issue #49): live/LIMITATIONS.md's "Exclusion
// cohorts" section names every cohort live/residue.go computes or curates,
// with counts and one-sentence reasons, so that "not covered" is a named,
// printable set rather than an implied one (#40's closing commitment).
//
// Seven spans, one per cohort, each rendered the same way SURVEY.md's own
// spans are: from committed data, byte-for-byte, with no provider and no
// network. Two cohorts (deprecated services, registry-laggard live
// services) and one roster (emulator-blocked) carry hand judgment residue.go
// documents in its own comments; this file only formats what residue.go
// already computed or curated, it does not re-derive any of it. tf-only and
// cfn-unmodeled (issue #53) are the two spans added alongside the existing
// five: the "unmapped Terraform types" cohort's own span narrowed to mean
// exactly what its live/mapping.json via ("none") still says - unclassified,
// not yet placed in any terminal class - once the taxonomy gave the
// mechanically- or curated-classified rows their own two spans instead.
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

// The seven residue-roster spans, one per exclusion cohort. Each lives in
// live/LIMITATIONS.md between a `<!-- survey-gen:begin NAME -->` line and a
// `<!-- survey-gen:end NAME -->` line, the same marker convention
// SURVEY.md's spans use (see render.go's spanMarkers).
const (
	spanResidueDeprecated   = "residue-deprecated"
	spanResidueCFNOnly      = "residue-cfn-only"
	spanResidueTFOnly       = "residue-tf-only"
	spanResidueCFNUnmodeled = "residue-cfn-unmodeled"
	spanResidueUnmapped     = "residue-unmapped"
	spanResidueLaggard      = "residue-laggard"
	spanResidueEmulator     = "residue-emulator"
)

// renderLimitationsMD rewrites live/LIMITATIONS.md's five residue-roster
// spans, from live/residue.go's accessors (which in turn read the committed
// live/mapping.json and live/registry.json), plus its untaggable-admitted
// span (issue #54, untaggable_render.go), from the committed
// live/survey-full.json and the compiled admission table.
func renderLimitationsMD(root string) error {
	mdPath := filepath.Join(root, limitationsMDRel)
	md, err := os.ReadFile(mdPath) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return err
	}

	untaggable, err := untaggableAdmittedTypes(root)
	if err != nil {
		return err
	}

	out, err := renderLimitationsSpans(string(md), untaggable)
	if err != nil {
		return err
	}
	if out == string(md) {
		fmt.Fprintf(os.Stderr, "survey-gen: %s's spans are already current\n", limitationsMDRel)
		return nil
	}
	if err := os.WriteFile(mdPath, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "survey-gen: rewrote %s's spans\n", limitationsMDRel)
	return nil
}

// renderLimitationsSpans returns live/LIMITATIONS.md with all five
// residue-roster spans and the untaggable-admitted span replaced by their
// rendered bodies. The rest of the file passes through byte-for-byte.
func renderLimitationsSpans(md string, untaggable []string) (string, error) {
	md, err := renderResidueSpans(md)
	if err != nil {
		return "", err
	}
	return replaceSpan(limitationsMDRel, md, spanUntaggableAdmitted, renderUntaggableAdmitted(untaggable))
}

// renderResidueSpans returns live/LIMITATIONS.md with all seven
// residue-roster spans replaced by their rendered bodies. The rest of the
// file passes through byte-for-byte.
func renderResidueSpans(md string) (string, error) {
	spans := []struct {
		name string
		body string
	}{
		{spanResidueDeprecated, renderResidueDeprecated()},
		{spanResidueCFNOnly, renderResidueCFNOnly()},
		{spanResidueTFOnly, renderResidueTFOnly()},
		{spanResidueCFNUnmodeled, renderResidueCFNUnmodeled()},
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
// count computed against live/registry.json, and the total. The closing
// paragraph adds issue #53's own TF-side count: how many live/mapping.json
// rows the mechanical deprecated-service classifier itself placed
// (via:"deprecated-service") - a subset of this cohort, since [Lookup]
// resolves every type under one of these prefixes to CohortDeprecated
// regardless of a mapping row's own via (a deprecated service AWS still
// ships a working CFN handler for, if any, is not swept into
// via:deprecated-service by the mechanical classifier - see
// tools/mapping-gen/taxonomy.go's deprecatedServiceEligible).
func renderResidueDeprecated() string {
	var b strings.Builder
	b.WriteString("| Service | TF prefix | CFN registry types | Reason |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, d := range residue.DeprecatedServices {
		fmt.Fprintf(&b, "| %s | `%s` | %d | %s |\n", d.Service, d.TFPrefix, residue.DeprecatedCount(d), d.Reason)
	}
	fmt.Fprintf(&b, "\n**Total.** %d CloudFormation Registry types across %d services.\n",
		residue.DeprecatedTotal(), len(residue.DeprecatedServices))
	fmt.Fprintf(&b, "\n%d Terraform types carry `live/mapping.json`'s own `via: \"deprecated-service\"` (issue #53): a TF prefix under one of the services above whose entire CFN Registry footprint ships no working handler at all, so a family sweep can never recover a real mapping for it either.\n",
		residue.DeprecatedServiceViaCount())
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

// renderResidueTFOnly renders the tf-only cohort (issue #53): every distinct
// note among live/mapping.json's via:"tf-only" rows, with its count, most
// common first - a provider-side construct (a waiter, a validation, an
// aws_ami_copy-style operation, a default_* adopter) with no cloud resource
// of its own.
func renderResidueTFOnly() string {
	groups, total := residue.TFOnlyGroups()
	var b strings.Builder
	b.WriteString("| Count | Note |\n")
	b.WriteString("|---|---|\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "| %d | %s |\n", g.Count, g.Note)
	}
	fmt.Fprintf(&b, "\n**Total.** %d Terraform AWS resource types that are provider-side constructs, not infrastructure - no CloudFormation counterpart is expected for any of them. Each row's own note is in `live/mapping.json`.\n", total)
	return b.String()
}

// renderResidueCFNUnmodeled renders the cfn-unmodeled cohort (issue #53):
// every distinct note among live/mapping.json's via:"cfn-unmodeled" rows,
// with its count - a real, live AWS resource the CloudFormation Registry
// does not model at all. Curated only today (no mechanical classifier
// promotes a row here - see tools/mapping-gen/overlay.json's own
// cfn_unmodeled table for why), so this table is empty until a family sweep
// adds its first entry.
func renderResidueCFNUnmodeled() string {
	groups, total := residue.CFNUnmodeledGroups()
	var b strings.Builder
	b.WriteString("| Count | Note |\n")
	b.WriteString("|---|---|\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "| %d | %s |\n", g.Count, g.Note)
	}
	fmt.Fprintf(&b, "\n**Total.** %d Terraform AWS resource types that are real infrastructure with no CloudFormation Registry model at all. Each row's own note is in `live/mapping.json`.\n", total)
	return b.String()
}

// renderResidueUnmapped renders the unclassified-TF-types cohort (narrowed
// by issue #53 to exactly what live/mapping.json's via:"none" still means):
// every distinct note among those rows, with its count, most common first -
// a TF type no mapping source and no terminal classifier, mechanical or
// curated, has placed yet.
func renderResidueUnmapped() string {
	groups, total := residue.UnmappedGroups()
	var b strings.Builder
	b.WriteString("| Count | Note |\n")
	b.WriteString("|---|---|\n")
	for _, g := range groups {
		fmt.Fprintf(&b, "| %d | %s |\n", g.Count, g.Note)
	}
	fmt.Fprintf(&b, "\n**Total.** %d Terraform AWS resource types with no CloudFormation Registry counterpart and no terminal classification yet - the count the family sweeps in issue #53's workplan burn down. Each row's own note is in `live/mapping.json`.\n", total)
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
