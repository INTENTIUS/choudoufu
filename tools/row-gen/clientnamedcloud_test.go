// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// The gap these tests close: a client-named proposal is one Component over
// one argument, and until setClientNamedEvidence ran, that component carried
// the argument and nothing else - no Default, no Cloud - however loudly the
// provider's own Argument Reference said that OMITTING the argument means
// the account the run is against or the provider's Region.
//
// The composite renderer had read that bullet since #241. The single-
// argument sibling never did, and the difference is invisible in every
// aggregate this repository keeps: the type is unadmitted either way, so no
// refusal count moves, and rowgen-convergence compares proposals against
// ratified rows of which none has this shape. It shows up only when someone
// pastes the proposal, and then only against a configuration that omits the
// argument - which for a per-account or per-region singleton is the ordinary
// case, and is what .corpus/s3-bucket/examples/account-public-access does.
//
// An identity component that renders "" is a WRONG marker, not a missing
// one: it names every account's singleton at once. That is the same failure
// identity.TestCloudSingletonNeedsDiscoveryWithNoRegionAnywhere guards on
// the rows that already carry the Cloud half.

// cloudDefaultClientNamed is every type whose proposal is client-named over
// an argument the provider's own docs give a cloud fallback for. It is
// computed from the two artifacts, never listed by hand: naming the types
// would make this test a restatement of the answer rather than a check on
// the rule.
func cloudDefaultClientNamed(t *testing.T) []proposal {
	t.Helper()
	proposals, grammar := loadAllForTest(t), loadImportGrammarForTest(t)
	var out []proposal
	for _, p := range proposals {
		if p.Bucket != bucketClientNamed || p.ArgName == "" {
			continue
		}
		g, ok := grammar[p.TFType]
		if !ok {
			continue
		}
		for _, a := range g.ArgumentReference {
			if a.Name == p.ArgName && a.CloudDefault != "" {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// TestClientNamedCarriesItsDocumentedCloudFallback is the rule itself: for
// every client-named proposal whose argument the docs give a cloud fallback,
// the proposal's ArgCloud must carry that same cloud property.
//
// The population is asserted non-empty first. A version of this test that
// looped over an empty slice would pass by seeing nothing, which is the
// blind-completeness shape that has caught this repository three times.
func TestClientNamedCarriesItsDocumentedCloudFallback(t *testing.T) {
	hits := cloudDefaultClientNamed(t)
	if len(hits) == 0 {
		t.Fatal("no client-named proposal reads a cloud-defaulting argument; " +
			"either live/import-grammar.json lost its cloud_default bullets or " +
			"the classifier stopped producing this shape - this test now proves nothing")
	}
	t.Logf("client-named proposals over a cloud-defaulting argument: %d", len(hits))
	grammar := loadImportGrammarForTest(t)
	for _, p := range hits {
		want := ""
		for _, a := range grammar[p.TFType].ArgumentReference {
			if a.Name == p.ArgName && a.CloudDefault != "" {
				want = a.CloudDefault
				break
			}
		}
		if got := p.ArgCloud[p.ArgName]; got != want {
			t.Errorf("%s: argument %q documents cloud_default %q; proposal carries %q",
				p.TFType, p.ArgName, want, got)
		}
	}
}

// TestClientNamedPastableRowSpellsTheCloudFallback is the half that decides
// whether an operator is affected: the RENDERED row, which is what a
// ratifier pastes unedited. attr() cannot carry a Cloud value at all, so a
// row still spelled attr(...) has dropped the evidence however well the
// proposal carries it - which is exactly how the eight shipped.
func TestClientNamedPastableRowSpellsTheCloudFallback(t *testing.T) {
	hits := cloudDefaultClientNamed(t)
	if len(hits) == 0 {
		t.Fatal("empty population; see TestClientNamedCarriesItsDocumentedCloudFallback")
	}
	for _, p := range hits {
		row := renderClientNamedEntry(p)
		if strings.Contains(row, "attr(") {
			t.Errorf("%s: pastable row still uses attr(), which cannot carry a Cloud fallback:\n%s", p.TFType, row)
			continue
		}
		want := `Cloud: "` + p.ArgCloud[p.ArgName] + `"`
		if !strings.Contains(row, want) {
			t.Errorf("%s: pastable row does not spell %s:\n%s", p.TFType, want, row)
		}
	}
}

// TestClientNamedWithoutACloudDefaultIsUnchanged is the other direction, and
// it is the one that keeps this from being a licence to decorate every row:
// a client-named proposal whose argument has NO documented fallback must
// still render the plain attr() form, byte for byte as before. Without this,
// a rule that fired on every client-named type would pass the two tests
// above.
func TestClientNamedWithoutACloudDefaultIsUnchanged(t *testing.T) {
	proposals, grammar := loadAllForTest(t), loadImportGrammarForTest(t)
	checked := 0
	for _, p := range proposals {
		if p.Bucket != bucketClientNamed || p.ArgName == "" {
			continue
		}
		if p.ArgCloud[p.ArgName] != "" || p.ArgDefaults[p.ArgName] != "" {
			continue
		}
		// Guard the guard: the argument really must have no bullet of
		// either kind, so this branch is "nothing to carry" and not
		// "something was dropped".
		g := grammar[p.TFType]
		if _, ok := g.OmittedFallbacks[p.ArgName]; ok {
			t.Fatalf("%s: argument %q documents an omitted fallback the proposal dropped", p.TFType, p.ArgName)
		}
		for _, a := range g.ArgumentReference {
			if a.Name == p.ArgName && a.CloudDefault != "" {
				t.Fatalf("%s: argument %q documents a cloud default the proposal dropped", p.TFType, p.ArgName)
			}
		}
		checked++
		if row := renderClientNamedEntry(p); !strings.Contains(row, "attr(") {
			t.Errorf("%s: no documented fallback, but the row was spelled in full:\n%s", p.TFType, row)
		}
	}
	if checked < 100 {
		t.Fatalf("only %d fallback-free client-named proposals checked; expected the great majority of the bucket", checked)
	}
	t.Logf("fallback-free client-named proposals still rendering attr(): %d", checked)
}
