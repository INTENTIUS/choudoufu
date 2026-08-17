// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The marker-governance render: live/MARKERS.md's "Granting an estate"
// section published an IAM model - "the marker is an ordinary resource tag,
// so IAM can condition on it directly through aws:ResourceTag, with no
// second permission model to keep in sync" - and named no type it cannot
// cover.
//
// It cannot cover an untaggable one. An object with no tags carries
// tofu-estate no more than it carries tofu-address, so a StringEquals on
// either key is unmatched and the published statement conveys nothing about
// that object. If a principal can act on it, the grant is broader than its
// condition, which is exactly the second permission model the claim says is
// absent. That holds for the across-estate grant (tofu-estate) and for the
// finer within-estate one (tofu-address) equally: they differ in which key
// they name, not in whether a tag is there to read.
//
// Which population, and why it is not the markerless veto
// ------------------------------------------------------
//
// identity.MarkerlessTypes is untaggable AND server-minted, and every type
// in it is outside the admission table by construction, so no estate ever
// contains one and no grant is written over one. The population the claim is
// wrong about is the other one: types the table ADMITS, which a configuration
// can declare and this fork will manage, that have no tags argument. Those
// are identified from their own declaration - the client-named,
// parent-derived and account-derived admission paths - which is a different
// question from whether IAM can condition on them. Being identifiable
// without a tag is not the same as being governable by one.
//
// So this derivation is the admission table joined to
// live/survey-full.json's taggability signal, which is exactly
// untaggableAdmittedTypes (untaggable_render.go, issue #54). It is reused
// rather than restated: live/LIMITATIONS.md's sweep entry and this section
// are two readings of one fact, and a second derivation of the same join
// would be free to drift from the first.
//
// The service attribution is live/mapping.json's own, read one step wider
// than registry.Roster.ServiceOf reads it. That accessor keeps only rows
// with a cfn_type, so a folded row - via "fold", cfn_type null, fold_parent
// naming the CloudFormation type it folds into - reports unmapped. For "can
// IAM condition on this" the fold parent's service segment is the right
// answer anyway, since the fold is about which CFN model describes the
// object and not about which service operates it. Reading fold_parent as
// well moves 62 of the 74 otherwise-unattributed types into a named service.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

const (
	// markersMDRel is the spec whose estate-grant section this file renders
	// two spans into.
	markersMDRel = "live/MARKERS.md"

	// spanGovernableCount is the inline span in the section's opening
	// sentence: how much of the admitted table the tag condition covers.
	// Inline because it sits mid-sentence (see replaceSpanInline).
	spanGovernableCount = "marker-governable-count"

	// spanGovernableGap is the block span of the subsection that names what
	// the grant cannot reach.
	spanGovernableGap = "marker-governable-gap"
)

// governanceService is one CloudFormation service's split of the admitted
// table: how many of its admitted types can carry a marker tag and how many
// cannot.
type governanceService struct {
	Service    string
	Untaggable int
	Admitted   int
}

// governanceSplit is the whole derivation, in one value, so the renderer and
// the drift test read the same thing.
type governanceSplit struct {
	// Untaggable and Taggable partition the admitted AWS resource types.
	// Their sum is deliberately smaller than the admission table: issue
	// #73's record-backed logical types are not AWS objects and are in
	// neither (see untaggableAdmittedTypes).
	Untaggable []string
	Taggable   []string

	// Services carries only the services with at least one untaggable
	// admitted type, sorted by that count and then by name.
	Services []governanceService

	// Unattributed is the untaggable types live/mapping.json can place in no
	// CloudFormation service, sorted. They are listed rather than dropped: a
	// service table that silently loses a third of its subject reads as a
	// complete one.
	Unattributed []string

	// Markerless is len(identity.MarkerlessTypes), carried so the paragraph
	// distinguishing the two populations states a measured number rather
	// than a remembered one.
	Markerless int
}

// deriveGovernanceSplit joins the admission table, live/survey-full.json and
// live/mapping.json.
func deriveGovernanceSplit(root string) (governanceSplit, error) {
	untaggable, taggable, err := untaggableAdmittedTypes(root)
	if err != nil {
		return governanceSplit{}, err
	}

	serviceOf, err := governanceServiceOf(root)
	if err != nil {
		return governanceSplit{}, err
	}

	admittedPerService := map[string]int{}
	for _, t := range append(append([]string{}, untaggable...), taggable...) {
		if svc := serviceOf(t); svc != "" {
			admittedPerService[svc]++
		}
	}

	untaggablePerService := map[string]int{}
	var unattributed []string
	for _, t := range untaggable {
		svc := serviceOf(t)
		if svc == "" {
			unattributed = append(unattributed, t)
			continue
		}
		untaggablePerService[svc]++
	}

	services := make([]governanceService, 0, len(untaggablePerService))
	for svc, n := range untaggablePerService {
		services = append(services, governanceService{Service: svc, Untaggable: n, Admitted: admittedPerService[svc]})
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].Untaggable != services[j].Untaggable {
			return services[i].Untaggable > services[j].Untaggable
		}
		return services[i].Service < services[j].Service
	})
	sort.Strings(unattributed)

	return governanceSplit{
		Untaggable:   untaggable,
		Taggable:     taggable,
		Services:     services,
		Unattributed: unattributed,
		Markerless:   len(identity.MarkerlessTypes),
	}, nil
}

// governanceServiceOf resolves a Terraform type to its CloudFormation
// service segment, reading a folded row's fold_parent as well as a mapped
// row's own cfn_type. It returns "" for a type live/mapping.json places
// nowhere, which the caller reports rather than absorbs.
func governanceServiceOf(root string) (func(string) string, error) {
	var mapping struct {
		Rows []struct {
			TFType     string  `json:"tf_type"`
			CFNType    *string `json:"cfn_type"`
			FoldParent *string `json:"fold_parent"`
		} `json:"rows"`
	}
	if err := readJSON(root, "live/mapping.json", &mapping); err != nil {
		return nil, err
	}
	svc := make(map[string]string, len(mapping.Rows))
	for _, row := range mapping.Rows {
		cfn := ""
		switch {
		case row.CFNType != nil && *row.CFNType != "":
			cfn = *row.CFNType
		case row.FoldParent != nil && *row.FoldParent != "":
			cfn = *row.FoldParent
		default:
			continue
		}
		if parts := strings.Split(cfn, "::"); len(parts) >= 2 && parts[1] != "" {
			svc[row.TFType] = parts[1]
		}
	}
	return func(tfType string) string { return svc[tfType] }, nil
}

// renderGovernableCount is the inline span: the covered fraction, stated as
// a fraction so a reader can see both halves of it at once.
func renderGovernableCount(s governanceSplit) string {
	return fmt.Sprintf("%d of the %d", len(s.Taggable), len(s.Untaggable)+len(s.Taggable))
}

// renderGovernableGap is the block span.
//
// The table is collapsed for the reason scpActionsSpanBody's is: 77 rows
// between two paragraphs buries the sentence that matters, and the sentence
// is what a reader has to leave with.
func renderGovernableGap(s governanceSplit) string {
	total := len(s.Untaggable) + len(s.Taggable)
	var b strings.Builder

	fmt.Fprintf(&b, "%d of the %d admitted AWS resource types carry no `tags` argument at all "+
		"(`live/survey-full.json`'s taggability signal, joined to the admission table). "+
		"A resource of one of those types carries `tofu-estate` no more than it carries "+
		"`tofu-address`, so both conditions above are unmatched on it and both statements "+
		"convey nothing about it. If a principal can act on such a resource, the grant is "+
		"wider than its condition, and keeping the two in step is a second permission model. "+
		"The top of this section says there is not one. There is, for these %d types.\n\n",
		len(s.Untaggable), total, len(s.Untaggable))

	fmt.Fprintf(&b, "This is not the markerless veto. The %d types in "+
		"`internal/live/identity`'s `MarkerlessTypes` are untaggable *and* server-minted, "+
		"and none of them is admitted, so no estate contains one. The %d here are admitted: "+
		"a configuration declares them and this fork manages them, identified from the "+
		"declaration itself rather than from a tag, which is what the client-named, "+
		"parent-derived and account-derived admission paths mean. Being identifiable "+
		"without a tag is a different property from being governable by one, and only the "+
		"second is what an IAM condition needs.\n\n",
		s.Markerless, len(s.Untaggable))

	fmt.Fprintf(&b, "They span %d CloudFormation services.\n\n", len(s.Services))

	b.WriteString("<details>\n<summary>Untaggable admitted types per service</summary>\n\n")
	b.WriteString("| Service | Untaggable | Admitted in this service |\n|---|---|---|\n")
	for _, svc := range s.Services {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", svc.Service, svc.Untaggable, svc.Admitted)
	}
	b.WriteString("\n</details>\n\n")

	if len(s.Unattributed) > 0 {
		fmt.Fprintf(&b, "%d further untaggable admitted types are absent from that table because "+
			"`live/mapping.json` places them in no CloudFormation service at all: %s. They are "+
			"named rather than dropped, because a service table that silently loses part of its "+
			"subject reads as a complete one.\n\n",
			len(s.Unattributed), joinWithAnd(backtickTypes(s.Unattributed), false))
	}

	b.WriteString("**What to use instead, for those types.** The reachable scope is the ordinary " +
		"one: a `Resource` ARN in the statement, the service's own resource policy, the account, " +
		"the region. That is coarser than a marker and it is maintained beside the estate instead " +
		"of by it, so it has to be revisited when the estate changes. This fork does not narrow " +
		"it and does not claim to.\n\n")

	fmt.Fprintf(&b, "**The count is a floor.** It is a fact about %d types, and a taggable type can "+
		"still go unmarked in one particular configuration - a resource declared inside a "+
		"`for_each`'d module body, a `tags` argument this pass can neither read nor merge into. "+
		"Those are properties of a configuration rather than of a type, so nothing here counts "+
		"them; `internal/live/stamp` reports each one as a skip when it happens.\n\n", total)

	b.WriteString("**One further limit, on the within-estate half only.** An escaped `tofu-address` " +
		"longer than one tag value is split across `tofu-address-2` through `tofu-address-4` (see " +
		"\"`tofu-address` continuation tags\"), so `StringEquals` on `aws:ResourceTag/tofu-address` " +
		"is compared against the first chunk alone. For such an address the condition is a prefix " +
		"test over a value this grammar says is meaningless on its own, and it should not be " +
		"written. The across-estate half is unaffected: `tofu-estate`'s own grammar caps it at 128 " +
		"characters, so it never splits.\n")

	return b.String()
}

// renderMarkersMD rewrites live/MARKERS.md's two governance spans.
func renderMarkersMD(root string) error {
	mdPath := filepath.Join(root, markersMDRel)
	md, err := os.ReadFile(mdPath) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return err
	}

	split, err := deriveGovernanceSplit(root)
	if err != nil {
		return err
	}

	out, err := renderGovernanceSpans(string(md), split)
	if err != nil {
		return err
	}
	if out == string(md) {
		fmt.Fprintf(os.Stderr, "survey-gen: %s's spans are already current\n", markersMDRel)
		return nil
	}
	if err := os.WriteFile(mdPath, []byte(out), 0o644); err != nil { //nolint:gosec // a committed doc, not a secret
		return err
	}
	fmt.Fprintf(os.Stderr, "survey-gen: rewrote %s's spans\n", markersMDRel)
	return nil
}

// renderGovernanceSpans returns live/MARKERS.md with both spans replaced.
// The rest of the file passes through byte-for-byte.
func renderGovernanceSpans(md string, s governanceSplit) (string, error) {
	md, err := replaceSpanInline(markersMDRel, md, spanGovernableCount, renderGovernableCount(s))
	if err != nil {
		return "", err
	}
	return replaceSpan(markersMDRel, md, spanGovernableGap, renderGovernableGap(s))
}
