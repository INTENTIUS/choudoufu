// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/terminal"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// terralithLedger is a ledger shaped like the estate GitHub issue #587 was
// filed from: tools/terralith-gen at scale 1, whose 79 instances split 38
// taggable / 41 untaggable (recomputed against live/readiness.json at this
// commit, and against the generator's own output: 21
// aws_iam_role_policy_attachment, 10 aws_route53_record, 10
// aws_iam_role_policy are the untaggable 41). The proportions matter to what
// this view has to get right, so the fixture keeps them rather than using
// three tidy rows.
func terralithLedger() StatelessAdoption {
	rep := StatelessAdoption{Estate: "tl1-terralith", Swept: true}

	add := func(n int, typeName string, taggable bool, class StatelessAdoptionClass, mutate func(*StatelessAdoptionRow)) {
		for i := 0; i < n; i++ {
			row := StatelessAdoptionRow{
				Addr:           fmt.Sprintf("%s.r%02d", typeName, i),
				TypeName:       typeName,
				Class:          class,
				CanCarryMarker: taggable,
			}
			if mutate != nil {
				mutate(&row)
			}
			rep.Rows = append(rep.Rows, row)
		}
	}

	// The untaggable 41. Every one of them derives its identity from a
	// parent that does carry a marker; six are waiting on a parent that has
	// not been adopted yet.
	add(15, "aws_iam_role_policy_attachment", false, AdoptionMarked, nil)
	add(6, "aws_iam_role_policy_attachment", false, AdoptionWaitsOnParent, nil)
	add(10, "aws_route53_record", false, AdoptionMarked, nil)
	add(10, "aws_iam_role_policy", false, AdoptionMarked, nil)

	// The taggable 38.
	add(17, "aws_iam_role", true, AdoptionMarked, nil)
	add(4, "aws_security_group", true, AdoptionAdoptable, func(r *StatelessAdoptionRow) {
		r.LiveID = "sg-625dfa25c07ed54c9"
		r.DisplayName = "tl1-ecs-sg"
		r.Matched = []StatelessTag{{Key: "name", Value: "tl1-ecs-sg"}}
		r.MarkerEstate = "tl1-terralith"
		r.MarkerAddress = "aws_security_group.ecs"
		r.Hint = "aws ec2 create-tags --resources 'sg-625dfa25c07ed54c9' --tags 'Key=tofu-estate,Value=tl1-terralith'"
	})
	add(7, "aws_iam_policy", true, AdoptionNoPath, func(r *StatelessAdoptionRow) {
		r.Detail = "IAM mints this policy's own ARN at create time, so no argument in the configuration reconstructs it."
	})
	add(1, "aws_ecs_cluster", true, AdoptionInTheWay, func(r *StatelessAdoptionRow) {
		r.LiveID = "tl1-cluster"
		r.HeldBy = "other-estate"
	})
	add(9, "aws_iam_instance_profile", true, AdoptionAbsent, nil)

	return rep
}

// TestAdoption_untaggableReadsAsOrdinary is the assertion GitHub issue #587
// exists for, and it is made on the RENDERED TEXT, never on a predicate: a
// majority-untaggable estate must not read as a majority-broken one.
//
// It pins three things about the untaggable 41 of 79:
//
//   - they are counted in their own headed half, above the marker half,
//     rather than appearing as an exception inside it;
//   - the words the view uses about them are not failure words;
//   - none of them is listed in an actionable section, because nothing is
//     asked of them.
//
// Proving it red: change the derived half's heading to a warning, move it
// below the marker half, or classify an untaggable row as NO_PATH, and the
// corresponding check fails.
func TestAdoption_untaggableReadsAsOrdinary(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	NewStatelessAdoption(NewView(streams).SetRunningInAutomation(true)).Adoption(terralithLedger())
	out := done(t).Stdout()

	// The population is stated as its own half of the estate, with both
	// numbers, before the marker half is reached.
	derived := strings.Index(out, "Identity by declaration: 41 of 79 instances")
	if derived < 0 {
		t.Fatalf("the untaggable half is not counted in its own heading:\n%s", out)
	}
	marker := strings.Index(out, "Identity by marker: 38 of 79 instances")
	if marker < 0 {
		t.Fatalf("the marker half is not counted in its own heading:\n%s", out)
	}
	if derived > marker {
		t.Errorf("the untaggable half is rendered after the marker half; on a real estate it is the larger of the two and reading it as a trailing exception is exactly what this view must not do")
	}

	// Its own types are named, so "41 need no marker" is concrete rather
	// than a bare count an operator has to take on trust.
	for _, want := range []string{
		"  21  aws_iam_role_policy_attachment\n",
		"  10  aws_route53_record\n",
		"  10  aws_iam_role_policy\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the derived half does not break down by type; want a line %q in:\n%s", want, out)
		}
	}

	// No failure vocabulary anywhere in the derived half's own prose. The
	// bound is the text between the two headings, so this cannot be
	// satisfied by wording elsewhere.
	half := out[derived:marker]
	for _, banned := range []string{
		"error", "Error", "fail", "Fail", "problem", "Problem",
		"unsupported", "not supported", "cannot be adopted", "warning", "Warning",
	} {
		if strings.Contains(half, banned) {
			t.Errorf("the untaggable half uses failure vocabulary %q; it is the majority of a real estate and the association/attachment/membership family working correctly:\n%s", banned, half)
		}
	}

	// And nothing is asked of them: no untaggable address appears under an
	// actionable heading.
	for _, section := range []string{"Adoptable now:", "No adoption path:", "In the way:"} {
		i := strings.Index(out, section)
		if i < 0 {
			continue
		}
		body := out[i:]
		for _, typeName := range []string{"aws_iam_role_policy_attachment", "aws_route53_record", "aws_iam_role_policy."} {
			if strings.Contains(body, typeName) {
				t.Errorf("section %q lists %s, a type that carries no marker and is asked for nothing", section, typeName)
			}
		}
	}
}

// TestAdoption_everyRowIsCountedOnce pins the ledger's arithmetic against
// the one failure this repository has watched aggregates make: a class that
// silently drops rows still adds up, because nobody checks the total. The
// two halves must sum to the population, and the marker half's own tally
// must sum to the marker half.
//
// Proving it red: drop a class from adoptionCarrierOrder, or give a row two
// classes, and one of the two sums stops matching.
func TestAdoption_everyRowIsCountedOnce(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	rep := terralithLedger()
	NewStatelessAdoption(NewView(streams).SetRunningInAutomation(true)).Adoption(rep)
	out := done(t).Stdout()

	if !strings.Contains(out, "Adoption: 79 declared resource instances, estate \"tl1-terralith\"") {
		t.Fatalf("the headline does not state the population and the estate:\n%s", out)
	}

	// Every class the marker half carries has to reach the tally, and the
	// tally has to sum to the marker half's own headline count.
	var carriers []StatelessAdoptionRow
	for _, r := range rep.Rows {
		if r.CanCarryMarker {
			carriers = append(carriers, r)
		}
	}
	sum := 0
	for _, c := range adoptionCarrierOrder {
		n := classCount(carriers, c)
		if n == 0 && !adoptionAlwaysShown[c] {
			continue
		}
		sum += n
		want := fmt.Sprintf("%4d  %-16s", n, adoptionClassLabel[c])
		if !strings.Contains(out, want) {
			t.Errorf("the marker tally has no line for %s; want %q in:\n%s", c, want, out)
		}
	}
	if sum != len(carriers) {
		t.Errorf("the rendered marker tally sums to %d, but the marker half holds %d instances: a class is missing from adoptionCarrierOrder and the ledger loses rows without saying so", sum, len(carriers))
	}
}

// TestAdoption_noPathIsStatedAsItsOwnSection pins the gap
// live/e2e/terralith-scale/MIGRATION.md found: 7 of 55 resources had no
// adoption path anywhere in the plan's output, buried as a [NEEDS_DISCOVERY]
// note in a 42-entry omission list. Here they are a headed section with the
// reason attached, which is the whole point of the issue.
//
// Proving it red: classify a NEEDS_DISCOVERY omission as ABSENT in
// statelessAdoptionReport and this section disappears.
func TestAdoption_noPathIsStatedAsItsOwnSection(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	NewStatelessAdoption(NewView(streams).SetRunningInAutomation(true)).Adoption(terralithLedger())
	out := done(t).Stdout()

	if !strings.Contains(out, "No adoption path: 7 resource instances") {
		t.Errorf("the no-adoption-path population is not its own section:\n%s", out)
	}
	if !strings.Contains(out, "IAM mints this policy's own ARN at create time") {
		t.Errorf("a no-path row does not carry the reason the projection gave for it:\n%s", out)
	}
	if !strings.Contains(out, "choudoufu live-import") {
		t.Errorf("the no-path section does not name the command that closes this case in bulk:\n%s", out)
	}
}

// TestAdoption_adoptableCarriesTheWholeContract pins that an adoptable row
// prints something an operator can act on without going anywhere else: the
// live object's handle, the paste-ready command where the type has one, and
// - always, command or not - both marker values, because the tag pair is the
// whole contract and a type with no printed command is not a type that
// cannot be adopted.
func TestAdoption_adoptableCarriesTheWholeContract(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	NewStatelessAdoption(NewView(streams).SetRunningInAutomation(true)).Adoption(terralithLedger())
	out := done(t).Stdout()

	for _, want := range []string{
		"Adoptable now: 4 resource instances",
		"sg-625dfa25c07ed54c9",
		"matched on: name=tl1-ecs-sg",
		"adopt with: aws ec2 create-tags",
		"or write: tofu-estate=tl1-terralith tofu-address=aws_security_group.ecs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("an adoptable row is missing %q:\n%s", want, out)
		}
	}
}

// TestAdoption_saysWhenNothingWasSwept guards the difference between "swept
// and found nothing to adopt" and "nothing was swept", which the foreign
// section already treats as different answers. An empty adoptable list under
// a run with no sweep behind it is not a report that there is nothing to
// adopt.
func TestAdoption_saysWhenNothingWasSwept(t *testing.T) {
	rep := terralithLedger()
	rep.Swept = false

	streams, done := terminal.StreamsForTesting(t)
	NewStatelessAdoption(NewView(streams).SetRunningInAutomation(true)).Adoption(rep)
	out := done(t).Stdout()

	if !strings.Contains(out, "No discovery sweep ran this time") {
		t.Errorf("a run with no sweep behind it does not say so:\n%s", out)
	}
}

// TestAdoption_ordinaryViewRendersNothing pins that the ledger is opt-in.
// The ordinary plan view already prints the same verdicts instance by
// instance in its own sections, and #587 is a complaint about that report's
// length, so adding a summary to it unasked would make the reported problem
// worse.
func TestAdoption_ordinaryViewRendersNothing(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	NewStatelessPlan(NewView(streams).SetRunningInAutomation(true)).Adoption(terralithLedger())
	out := done(t)

	if got := out.Stdout(); got != "" {
		t.Errorf("the ordinary plan view rendered the adoption ledger unasked:\n%s", got)
	}
	if got := out.Stderr(); got != "" {
		t.Errorf("the ordinary plan view wrote to stderr for an adoption ledger:\n%s", got)
	}
}

// TestAdoption_onlyModeRendersOnlyTheLedger pins the "-only" half of the
// mode's name. Every other section is a deliberate no-op on this view, so a
// pipeline that calls them all still prints one section.
func TestAdoption_onlyModeRendersOnlyTheLedger(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessAdoption(NewView(streams).SetRunningInAutomation(true))

	v.Omissions([]StatelessOmission{{Addr: "aws_vpc.main", Reason: "ABSENT", Detail: "not there"}})
	v.Unowned([]StatelessUnowned{{Addr: "aws_iam_role.app", TypeName: "aws_iam_role", LiveID: "app-role", MarkerEstate: "dev", MarkerAddress: "aws_iam_role.app"}})
	v.Foreign(StatelessForeign{Estate: "dev", Swept: []string{"aws_vpc"}})
	v.GuidedFallback("the hint was stale")
	v.Lookalikes([]StatelessLookalike{{Addr: "aws_vpc.main", TypeName: "aws_vpc", LiveID: "vpc-1"}})

	out := done(t).Stdout()
	if out != "" {
		t.Errorf("-adoption-only printed a section other than the ledger:\n%s", out)
	}
}

// TestAdoptionOnlyPlan_warningsAreNamedNotHidden pins the difference between
// compacting a warning and hiding it. Measured against live/e2e/estate-block
// on the pinned emulator, 470 of the 500 lines left after the diff came out
// were warning bodies - 36 of them one per provider type the emulator cannot
// list - so leaving them in full would have left #587's reported problem
// standing. Every one is still named; only the body goes.
//
// Proving it red: drop the summary loop and print only a count, and the
// per-summary assertions fail; drop the "run the same command without" line
// and the reader is left with no way back to the detail.
func TestAdoptionOnlyPlan_warningsAreNamedNotHidden(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	view := NewView(streams).SetRunningInAutomation(true)

	var diags tfdiags.Diagnostics
	for i := 0; i < 36; i++ {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning,
			"Incomplete sweep for undeclared resources",
			"a very long body naming a provider type this estate never declares, repeated once per type"))
	}
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning,
		"Resource type has no orphan recovery", "another long body"))

	kept := compactWarnings(view, diags)
	if len(kept) != 0 {
		t.Errorf("compactWarnings kept %d warnings for full rendering; the bodies are what this mode drops", len(kept))
	}

	out := done(t).Stdout()
	for _, want := range []string{
		"37 warnings were withheld by -adoption-only",
		"36x  Incomplete sweep for undeclared resources",
		"Resource type has no orphan recovery",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the compact warning list is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(out), " "), "Run the same command without -adoption-only to read them in full") {
		t.Errorf("the compact list does not say how to read the withheld warnings in full:\n%s", out)
	}
	// The whole point: the bodies are gone, so this is short.
	if n := strings.Count(out, "\n"); n > 20 {
		t.Errorf("the compact warning list is %d lines; it exists to replace 470", n)
	}
	if strings.Contains(out, "a very long body naming a provider type") {
		t.Errorf("a warning body survived compaction:\n%s", out)
	}
}

// TestAdoptionOnlyPlan_errorsAreNeverWithheld is the safety half. A mode
// that summarizes adoption while swallowing a failure would be worse than
// the noise it removes, so an error is passed through untouched and is
// never counted among the withheld.
func TestAdoptionOnlyPlan_errorsAreNeverWithheld(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	view := NewView(streams).SetRunningInAutomation(true)

	var diags tfdiags.Diagnostics
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Marker collision", "two live resources claim one address"))
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Incomplete sweep for undeclared resources", "body"))

	kept := compactWarnings(view, diags)
	if len(kept) != 1 || kept[0].Severity() != tfdiags.Error {
		t.Fatalf("compactWarnings returned %d diagnostics; the error must survive untouched", len(kept))
	}
	if got := kept[0].Description().Summary; got != "Marker collision" {
		t.Errorf("the surviving diagnostic is %q, not the error", got)
	}

	out := done(t).Stdout()
	if !strings.Contains(out, "1 warning was withheld") {
		t.Errorf("the withheld count includes the error, or is missing:\n%s", out)
	}
	if strings.Contains(out, "Marker collision") {
		t.Errorf("an error was compacted into the withheld list:\n%s", out)
	}
}

// TestAdoptionOnlyPlan_noViewMeansNoCompaction pins the fallback: with
// nowhere to write the compact list, every diagnostic passes through in
// full. Dropping bodies with nothing printed in their place is the silent
// hiding this mode must never do.
func TestAdoptionOnlyPlan_noViewMeansNoCompaction(t *testing.T) {
	var diags tfdiags.Diagnostics
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Incomplete sweep for undeclared resources", "body"))

	if kept := compactWarnings(nil, diags); len(kept) != 1 {
		t.Errorf("with no view to write to, compactWarnings returned %d of 1 diagnostics", len(kept))
	}
}
