// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers/markerstest"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/terminal"
)

// GitHub issue #587's builder tests. Every assertion here is made on the
// RENDERED LEDGER rather than on a returned struct field, for the reason the
// issue names: this repository has had a predicate read true while the marker
// it stood for was wrong, six times, and the operator reads text.

// flattenSpace collapses every run of whitespace to one space, so an
// assertion about a SENTENCE is not really an assertion about where the word
// wrapper happened to break the line at this terminal width.
func flattenSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// adoptionAddr parses an instance address or fails the test.
func adoptionAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("parse %q: %s", s, diags.Err())
	}
	return addr
}

// renderAdoption builds the ledger and renders it through the adoption-only
// view, returning what an operator would see on stdout.
func renderAdoption(t *testing.T, rep views.StatelessAdoption) string {
	t.Helper()
	streams, done := terminal.StreamsForTesting(t)
	views.NewStatelessAdoption(views.NewView(streams).SetRunningInAutomation(true)).Adoption(rep)
	return done(t).Stdout()
}

// adoptionSchemas is the schema map the builder consults, and only through
// markers.Taggable.
//
// aws_thing_tagged is an ordinary free-form tags map. aws_thing_attachment
// has no tags attribute at all - the association/attachment/membership shape.
// aws_thing_vocabulary is the trap: a tags map of exactly the same shape as
// the first, whose keys the provider has documented as its own namespace.
// markers.Taggable refuses it and the four-clause copies this repository used
// to carry all admitted it (internal/live/markers/markerstest's own package
// comment), so a row for it landing in the marker half is proof that the
// build has grown a second taggability rule.
func adoptionSchemas() map[string]providers.Schema {
	return map[string]providers.Schema{
		"aws_thing_tagged":     {Block: markerstest.FreeFormTagsBlock()},
		"aws_thing_vocabulary": {Block: markerstest.VocabularyRefusedBlock()},
		"aws_thing_attachment": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{}}},
	}
}

// TestStatelessAdoptionReport_taggabilityIsMarkersTaggable pins that the one
// judgement this file makes for itself is not made for itself at all: it is
// markers.Taggable, the single implementation every writing package in this
// repository delegates to, live-import's UNTAGGABLE verdict included.
//
// Proving it red: replace the markers.Taggable call in
// statelessAdoptionReport with the "has a settable top-level tags attribute"
// shape check that used to be inlined in four places, and
// aws_thing_vocabulary moves from the declaration half to the marker half.
func TestStatelessAdoptionReport_taggabilityIsMarkersTaggable(t *testing.T) {
	res := &projection.Result{
		Materialized: []addrs.AbsResourceInstance{
			adoptionAddr(t, "aws_thing_tagged.a"),
			adoptionAddr(t, "aws_thing_vocabulary.b"),
			adoptionAddr(t, "aws_thing_attachment.c"),
		},
	}

	out := renderAdoption(t, statelessAdoptionReport(res, views.StatelessForeign{}, nil, adoptionSchemas(), "dev", true))

	if !strings.Contains(out, "Identity by declaration: 2 of 3 instances") {
		t.Errorf("the declaration half does not hold both untaggable types; a vocabulary-namespaced tags map is not a marker surface (markers.TagSurface's VocabularyRefusal clause):\n%s", out)
	}
	if !strings.Contains(out, "Identity by marker: 1 of 3 instances") {
		t.Errorf("the marker half does not hold exactly the one free-form tags map:\n%s", out)
	}
	for _, want := range []string{"1  aws_thing_attachment", "1  aws_thing_vocabulary"} {
		if !strings.Contains(out, want) {
			t.Errorf("the declaration half's type tally is missing %q:\n%s", want, out)
		}
	}
}

// TestStatelessAdoptionReport_readsTheUnownedSectionsOwnVerdict pins that an
// adoptable resource is adoptable here because the Unowned section already
// said so, values and all, rather than because this file decided it a second
// time. statelessUnownedReport is what both consume.
//
// Proving it red: compute MarkerEstate here instead of reading it, and a run
// with no estate name starts offering adoptions the Unowned section refuses
// to offer.
func TestStatelessAdoptionReport_readsTheUnownedSectionsOwnVerdict(t *testing.T) {
	res := &projection.Result{
		Omitted: []projection.Omission{
			{Addr: adoptionAddr(t, "aws_thing_tagged.mine"), Reason: projection.ReasonUnowned, Detail: "unmarked"},
			{Addr: adoptionAddr(t, "aws_thing_tagged.theirs"), Reason: projection.ReasonUnowned, Detail: "unmarked"},
		},
		Unowned: []projection.Unowned{
			{Addr: adoptionAddr(t, "aws_thing_tagged.mine"), TypeName: "aws_thing_tagged", ImportID: "thing-1"},
			{Addr: adoptionAddr(t, "aws_thing_tagged.theirs"), TypeName: "aws_thing_tagged", ImportID: "thing-2", Estate: "other"},
		},
	}
	unowned := statelessUnownedReport(res, "dev")

	out := renderAdoption(t, statelessAdoptionReport(res, views.StatelessForeign{}, unowned, adoptionSchemas(), "dev", true))

	if !strings.Contains(out, "Adoptable now: 1 resource instance") {
		t.Errorf("the unmarked resource is not offered for adoption:\n%s", out)
	}
	if !strings.Contains(out, "or write: tofu-estate=dev tofu-address=aws_thing_tagged.mine") {
		t.Errorf("the adoption line does not carry the marker pair the Unowned section computed:\n%s", out)
	}
	if !strings.Contains(out, "In the way: 1 resource instance") || !strings.Contains(out, `held by estate "other"`) {
		t.Errorf("a resource another estate holds is not reported as in the way:\n%s", out)
	}
	// The one that belongs to another estate must never be offered.
	if strings.Contains(out, "tofu-address=aws_thing_tagged.theirs") {
		t.Errorf("the ledger offers to adopt a resource another estate owns:\n%s", out)
	}
}

// TestStatelessAdoptionReport_contentMatchWins pins that the foreign
// classifier's content match - the one adoption verdict that arrives with a
// paste-ready command attached - is not lost behind the omission reason that
// made the instance eligible for classification in the first place.
//
// Proving it red: move the candidate lookup below the switch, and an
// instance the classifier matched renders as "nothing live" with no command.
func TestStatelessAdoptionReport_contentMatchWins(t *testing.T) {
	res := &projection.Result{
		Omitted: []projection.Omission{
			{Addr: adoptionAddr(t, "aws_thing_tagged.zone"), Reason: projection.ReasonAbsent, Detail: "no such object"},
		},
	}
	foreignRep := views.StatelessForeign{
		Estate: "dev",
		Candidates: []views.StatelessBindCandidate{{
			Addr:          "aws_thing_tagged.zone",
			TypeName:      "aws_thing_tagged",
			LiveID:        "Z6ULYQAYZAD0GR7",
			DisplayName:   "example.test",
			Matched:       []views.StatelessTag{{Key: "name", Value: "example.test"}},
			MarkerEstate:  "dev",
			MarkerAddress: "aws_thing_tagged.zone",
			Hint:          "aws ec2 create-tags --resources 'Z6ULYQAYZAD0GR7'",
		}},
	}

	out := renderAdoption(t, statelessAdoptionReport(res, foreignRep, nil, adoptionSchemas(), "dev", true))

	for _, want := range []string{
		"Adoptable now: 1 resource instance",
		"Z6ULYQAYZAD0GR7",
		"matched on: name=example.test",
		"adopt with: aws ec2 create-tags --resources 'Z6ULYQAYZAD0GR7'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the content-matched adoption is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "nothing live     1") {
		t.Errorf("a content-matched instance is also counted as having nothing live:\n%s", out)
	}
}

// TestStatelessAdoptionReport_needsDiscoveryIsItsOwnAnswer is the gap
// live/e2e/terralith-scale/MIGRATION.md measured: 7 of 55 resources had no
// adoption path anywhere in the plan output, because a NEEDS_DISCOVERY
// omission is a line in a 42-entry "Not read from the live system" list and
// nothing else. Here it is a named class with its own section.
//
// Proving it red: route ReasonNeedsDiscovery to AdoptionAbsent, and the
// section vanishes while the count moves to "nothing live" - the exact
// misreading the issue was filed about.
func TestStatelessAdoptionReport_needsDiscoveryIsItsOwnAnswer(t *testing.T) {
	res := &projection.Result{
		Omitted: []projection.Omission{
			{
				Addr:   adoptionAddr(t, "aws_thing_tagged.policy"),
				Reason: projection.ReasonNeedsDiscovery,
				Detail: "IAM mints this policy's own ARN at create time.",
			},
			{
				Addr:   adoptionAddr(t, "aws_thing_attachment.attach"),
				Reason: projection.ReasonParentUnavailable,
				Detail: "aws_thing_tagged.policy is not in the projection.",
			},
		},
	}

	out := renderAdoption(t, statelessAdoptionReport(res, views.StatelessForeign{}, nil, adoptionSchemas(), "dev", true))

	if !strings.Contains(out, "No adoption path: 1 resource instance") {
		t.Errorf("a server-assigned identity with nothing to point at is not reported as having no adoption path:\n%s", out)
	}
	if !strings.Contains(out, "IAM mints this policy's own ARN at create time.") {
		t.Errorf("the no-path row does not carry the projection's own reason:\n%s", out)
	}

	// The attachment waiting on it is untaggable, so it must land in the
	// declaration half with the "adopt the parent" note - never in an
	// actionable section, because nothing is asked of it.
	if !strings.Contains(flattenSpace(out), "1 of them cannot be resolved yet because a parent they derive from is not resolved either.") {
		t.Errorf("a parent-derived instance waiting on its parent is not explained as such:\n%s", out)
	}
	if i := strings.Index(out, "No adoption path:"); i >= 0 && strings.Contains(out[i:], "aws_thing_attachment") {
		t.Errorf("an untaggable, parent-derived instance is listed as having no adoption path; it needs no marker and the parent is the only thing to act on:\n%s", out)
	}
}

// TestStatelessAdoptionReport_countsTheWholePopulation pins that the ledger
// is a partition of what the projection attempted: one row per materialized
// instance plus one per omission, no more and no fewer. An aggregate that
// silently drops a class still adds up, which is how this repository has had
// a defect reported as an improvement before.
//
// Proving it red: skip any arm of the switch, or return early on an
// unrecognised reason, and the headline count stops matching.
func TestStatelessAdoptionReport_countsTheWholePopulation(t *testing.T) {
	res := &projection.Result{
		Materialized: []addrs.AbsResourceInstance{adoptionAddr(t, "aws_thing_tagged.a")},
		Omitted: []projection.Omission{
			{Addr: adoptionAddr(t, "aws_thing_tagged.b"), Reason: projection.ReasonAbsent},
			{Addr: adoptionAddr(t, "aws_thing_tagged.c"), Reason: projection.ReasonNeedsDiscovery},
			{Addr: adoptionAddr(t, "aws_thing_tagged.d"), Reason: projection.ReasonFailed, Detail: "the provider errored"},
			{Addr: adoptionAddr(t, "aws_thing_tagged.e"), Reason: projection.ReasonCycle, Detail: "a dependency cycle"},
			{Addr: adoptionAddr(t, "aws_thing_attachment.f"), Reason: projection.ReasonParentUnavailable},
			{Addr: adoptionAddr(t, "aws_thing_attachment.g"), Reason: projection.ReasonSuperseded},
		},
	}

	rep := statelessAdoptionReport(res, views.StatelessForeign{}, nil, adoptionSchemas(), "dev", true)
	if got, want := len(rep.Rows), len(res.Materialized)+len(res.Omitted); got != want {
		t.Fatalf("the ledger holds %d rows for %d attempted instances", got, want)
	}

	out := renderAdoption(t, rep)
	if !strings.Contains(out, "Adoption: 7 declared resource instances") {
		t.Errorf("the headline does not count every attempted instance:\n%s", out)
	}
	// FAILED and CYCLE have no adoption answer of their own, so they are
	// reported rather than quietly folded into "nothing live", which would
	// have the plan propose creating something that may well exist.
	if !strings.Contains(out, "Not read: 2 resource instances") {
		t.Errorf("an instance the projection could not read is not reported:\n%s", out)
	}
	for _, want := range []string{"the provider errored", "a dependency cycle"} {
		if !strings.Contains(out, want) {
			t.Errorf("a not-read row does not carry its reason %q:\n%s", want, out)
		}
	}
}

// TestPlanRejectAdoptionOnly pins that -adoption-only is refused rather than
// ignored on a state-backed run. A flag that silently does nothing would
// hand an operator the ordinary plan they were trying to avoid, with no sign
// that they had asked for anything else.
func TestPlanRejectAdoptionOnly(t *testing.T) {
	if diags := planRejectAdoptionOnly(true, false); !diags.HasErrors() {
		t.Errorf("-adoption-only was accepted on a run with no live block")
	} else if got := diags.Err().Error(); !strings.Contains(got, "live block") {
		t.Errorf("the refusal does not say what is missing: %s", got)
	}
	if diags := planRejectAdoptionOnly(true, true); diags.HasErrors() {
		t.Errorf("-adoption-only was refused on a live-markers run: %s", diags.Err())
	}
	if diags := planRejectAdoptionOnly(false, false); diags.HasErrors() {
		t.Errorf("an ordinary plan was refused: %s", diags.Err())
	}
}
