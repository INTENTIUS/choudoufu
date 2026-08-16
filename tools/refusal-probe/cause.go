// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/stamp"
)

// A count of stamp sites is much less useful than a breakdown of why they
// refused, and reading the reason back out is the job every hand-built probe
// was actually doing. This file is that, derived rather than tabulated.
//
// [check.Analyze] runs the stamp pass and keeps its diagnostics, but discards
// the [stamp.Result] those diagnostics came from - so the structured
// [identity.DiscoveryCause] behind an "Unmarked apply of a marker-only
// resource" is not on the report. The only thing that reaches a consumer is
// the rendered sentence.
//
// Matching that sentence against a table of strings written out by hand is
// the obvious way to recover the cause and the wrong one: the table would
// agree with the wording on the day it was typed and silently stop agreeing
// afterwards, which is this repository's own ratchet-measuring-itself shape.
// So the fingerprints are RENDERED, at startup, by calling the same exported
// function that renders them for a user - [stamp.UnmarkedDiscoveryDetail] -
// once per [identity.AllDiscoveryCauses] entry, with sentinel substitutions.
// Change the wording and this file follows it. Add a cause and it appears
// here without an edit. Break the correspondence and [causeCatalog.validate]
// fails at startup rather than mislabelling every site in the run.

// The sentinel subject and subjects. Nothing about them is special except
// that they must not occur in a real configuration, and that
// [identity.CloudValue.Describe] must echo the argument back rather than
// translate it - it does, for any value that is not one of its own
// constants, which is what makes the substituted region locatable.
const (
	sentinelType = "PROBEZZTYPE"
	sentinelName = "PROBEZZNAME"
	sentinelArg0 = "PROBEZZARGZERO"
	sentinelArg1 = "PROBEZZARGONE"
)

// causeUnclassified is the honest answer for a detail that matches no
// rendered sentence. It is reported rather than folded into any real cause:
// a site whose reason this program could not read must not be counted as one
// whose reason it could.
const causeUnclassified = "UNCLASSIFIED"

// fingerprint is one literal run of a rendered sentence that survives every
// substitution, and the cause label a detail containing it carries.
type fingerprint struct {
	fragment string
	label    string
}

// causeCatalog classifies a refused site by cause.
type causeCatalog struct {
	prints []fingerprint

	// samples are the renderings the catalog was built from, kept so
	// validate can re-classify each one and prove the catalog reproduces
	// the labels it was derived from.
	samples map[string]string
}

// newCauseCatalog renders one sentence per discovery cause per subject count
// and reduces each to its longest substitution-free fragment.
//
// The subject counts matter: [stamp.UnmarkedDiscoveryDetail] switches on how
// many subjects arrived alongside the cause, and a cause whose subjects did
// not arrive falls through to the sentence every cause shared before they
// were told apart. Those fall-throughs are collected under one label naming
// every cause that produces them, because that is what the sentence actually
// says - a fall-through detail is evidence for the whole set and it would be
// a fabrication to attribute it to any single member.
func newCauseCatalog() (*causeCatalog, error) {
	addr := addrs.ConfigResource{
		Module: addrs.RootModule,
		Resource: addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: sentinelType,
			Name: sentinelName,
		},
	}
	subs := []string{addr.String(), sentinelArg0, sentinelArg1}
	args := []string{sentinelArg0, sentinelArg1}

	fallthroughText := stamp.UnmarkedDiscoveryDetail(addr, identity.BlockDiscovery{})

	c := &causeCatalog{samples: map[string]string{}}
	byFragment := map[string]string{}
	var fellThrough []string
	seenFellThrough := map[string]bool{}

	for _, cause := range identity.AllDiscoveryCauses() {
		for argc := 0; argc <= len(args); argc++ {
			rendered := stamp.UnmarkedDiscoveryDetail(addr, identity.BlockDiscovery{
				Cause: cause,
				Args:  args[:argc],
			})
			if rendered == fallthroughText {
				if !seenFellThrough[string(cause)] {
					seenFellThrough[string(cause)] = true
					fellThrough = append(fellThrough, string(cause))
				}
				continue
			}
			frag := longestFragment(rendered, subs)
			if frag == "" {
				return nil, fmt.Errorf("cause %s with %d subject(s) rendered no substitution-free fragment", cause, argc)
			}
			if prior, ok := byFragment[frag]; ok && prior != string(cause) {
				return nil, fmt.Errorf("causes %s and %s render the same fragment %q, so no detail can distinguish them",
					prior, cause, frag)
			}
			byFragment[frag] = string(cause)
			c.samples[rendered] = string(cause)
		}
	}

	sort.Strings(fellThrough)
	fallLabel := strings.Join(fellThrough, "|")
	if fallLabel == "" {
		return nil, fmt.Errorf("no cause rendered the fall-through sentence, which %s's default branch says must exist", "UnmarkedDiscoveryDetail")
	}
	fallFrag := longestFragment(fallthroughText, subs)
	if fallFrag == "" {
		return nil, fmt.Errorf("the fall-through sentence rendered no substitution-free fragment")
	}
	byFragment[fallFrag] = fallLabel
	c.samples[fallthroughText] = fallLabel

	for frag, label := range byFragment {
		c.prints = append(c.prints, fingerprint{fragment: frag, label: label})
	}
	// Longest first: a longer fragment is the more specific claim, and the
	// order makes the -v output deterministic.
	sort.Slice(c.prints, func(i, j int) bool {
		if len(c.prints[i].fragment) != len(c.prints[j].fragment) {
			return len(c.prints[i].fragment) > len(c.prints[j].fragment)
		}
		return c.prints[i].fragment < c.prints[j].fragment
	})

	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// validate re-classifies every sentence the catalog was derived from and
// requires each to come back as its own label.
//
// This is the check that keeps the catalog from being a ratchet against
// itself. It cannot prove the labels are right - only the renderer can say
// that - but it does prove that two causes have not become indistinguishable,
// which is the failure that would otherwise show up as a plausible-looking
// breakdown built on a coin toss.
func (c *causeCatalog) validate() error {
	for rendered, want := range c.samples {
		if got := c.discovery(rendered); got != want {
			return fmt.Errorf("the rendered sentence for %s classifies as %s; the fingerprints do not reproduce their own source", want, got)
		}
	}
	return nil
}

// discovery reads the discovery cause back out of one rendered detail.
func (c *causeCatalog) discovery(detail string) string {
	var labels []string
	seen := map[string]bool{}
	for _, p := range c.prints {
		if !strings.Contains(detail, p.fragment) {
			continue
		}
		if seen[p.label] {
			continue
		}
		seen[p.label] = true
		labels = append(labels, p.label)
	}
	switch len(labels) {
	case 0:
		return causeUnclassified
	case 1:
		return labels[0]
	default:
		sort.Strings(labels)
		return "AMBIGUOUS(" + strings.Join(labels, ",") + ")"
	}
}

// site is the cause of one refused site, as "<axis>:<value>".
//
// Three axes, in the order of how much they say, and every one of them read
// off something structured rather than guessed:
//
//   - discovery, for a stamp-layer site: the cause behind an unmarked
//     marker-only apply, recovered as above. This is the axis three agents
//     needed and could not get.
//   - reference, from [check.Site.Category], which the static-reference
//     refusals already carry as [configs.ReferenceCategory] off the raising
//     diagnostic's Extra field.
//   - type, from [check.Site.Type], set by the type-shaped lint rules. For
//     unadmitted-type this is the demand breakdown: which types, not how
//     many sites.
//
// An empty string means this site carries none of the three. It is counted
// under its own key rather than dropped, so the breakdown always sums to the
// site count and a reader can see how much of it is unexplained.
//
// The discovery axis is applied to the one stamp summary that actually
// carries a discovery sentence, not to the stamp layer as a whole: a marker
// CONFLICT is also a stamp-layer error and has no cause of this kind, so
// labelling it "discovery:UNCLASSIFIED" would put it on an axis it was never
// on. Nothing in this corpus raises one today, which is exactly why the guard
// is worth having now rather than after one does.
func (c *causeCatalog) site(layer check.Layer, s check.Site, id string) string {
	switch {
	case layer == check.LayerStamp && id == stamp.SummaryUnmarkedApply:
		return "discovery:" + c.discovery(s.Detail)
	case s.Category != "":
		return "reference:" + string(s.Category)
	case s.Type != "":
		return "type:" + s.Type
	default:
		return ""
	}
}

// longestFragment returns the longest run of text in s that no substitution
// falls inside, which is the part of a rendered sentence that is the same for
// every subject it could have been rendered with.
func longestFragment(s string, subs []string) string {
	parts := []string{s}
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		var next []string
		for _, p := range parts {
			next = append(next, strings.Split(p, sub)...)
		}
		parts = next
	}
	var best string
	for _, p := range parts {
		if len(p) > len(best) {
			best = p
		}
	}
	return best
}
