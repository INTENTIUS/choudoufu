// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "strings"

// Stage is one step of the gauntlet. The registry below is the only place a
// stage is described: live/GAUNTLET.md, the site's progress pages and the
// artifact schema are all rendered from it, and TestRenderedDocsAreCurrent
// fails when they drift.
//
// Every stage is a comparison against stock OpenTofu. "Proves" says what a
// pass means, "Oracle" says what stock's answer to the same question is and
// how it is compared, and "Break" says how a script demonstrates that its
// check is load-bearing (BREAK=1 must make the stage fail). A stage with
// Status "planned" is declared so the docs name it, but no estate is held to
// it: it does not count toward "clear" until its Status is flipped to
// "active".
//
// Headline is a second, independent axis: whether an active stage counts
// toward the two headline bars (core/all estates clear) and toward what
// `next` selects as work. Every stage that gates the bars carries
// Headline: true. A stage whose own Proves text says it is "tested and
// shown per estate; not part of the headline bars" (currently "strict")
// carries Headline: false: flipping its Status to active starts it running
// and being reported per estate, without moving either bar and without
// `next` ever picking it as the unit to fix (#482 - before this field
// existed, isClear and NextUnits keyed strictly off Status, so flipping such
// a stage active would have silently widened what the bars gate on, far
// past what the stage's own docs claim). For a headline stage, flipping
// Status to active is still the deliberate change that lowers the bars
// until estates catch up.
//
// Tier1Gated is a third, independent axis (#999): whether a headline
// stage's activation evidence is a tier-1 fixture (live/behaviors.json,
// #522's ruling) rather than 26 hand-written per-estate sections. #491 and
// #643 retired the sweep model that used to supply those sections, so an
// estate with no section for such a stage is not evidence the estate
// fails it - it is evidence the estate has never been asked to run it.
// isClearAgainst (artifact.go) treats a "not_run" verdict on a
// Tier1Gated stage as neutral rather than as a miss: an estate that never
// exercises it stays clear. A genuine "fail" still fails it, and a genuine
// per-estate "pass" - reference-ec2-vpc's day2_crash, a survivor of the
// retired sweep model - still counts, exactly as it would for any other
// headline stage. This is the maintainer's ruling on #999 (option 2 over
// "tier-1 stages never gate clear" - chosen specifically so a real,
// already-recorded pass like that one keeps counting instead of being
// discarded). Every other headline stage (Tier1Gated: false) is unaffected:
// a "not_run" on it still breaks clear, exactly as it always has.
type Stage struct {
	ID         string `json:"id"`
	Order      int    `json:"order"`
	Title      string `json:"title"`
	Status     string `json:"status"`      // "active" or "planned"
	Headline   bool   `json:"headline"`    // counts toward the two bars and toward `next`, once active
	Tier1Gated bool   `json:"tier1_gated"` // "not_run" is neutral for `clear`, not a miss; "fail" still fails it (#999)
	Proves     string `json:"proves"`
	Oracle     string `json:"oracle"`
	Break      string `json:"break"`
	// Substrates says how the stage reads on a substrate other than the
	// floci emulator (#1067), keyed by substrate name (SubstrateKind). A
	// stage absent from the map applies there exactly as written. A note
	// beginning "n/a: " means the stage cannot be run on that substrate:
	// Rebuild (artifact.go) records it as VerdictNA for every estate on
	// that substrate, neutral for clear, and the reason renders beside the
	// stage rather than the stage being skipped silently. Any other note is
	// that substrate's own oracle, rendered under the stage's Oracle line.
	Substrates map[string]string `json:"substrates,omitempty"`
}

// NotApplicable reports whether the stage cannot run on substrate, and why.
func (s Stage) NotApplicable(substrate string) (reason string, na bool) {
	note, ok := s.Substrates[substrate]
	if !ok || !strings.HasPrefix(note, naPrefix) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(note, naPrefix)), true
}

// naPrefix marks a Substrates note as "this stage does not apply here".
const naPrefix = "n/a:"

const (
	StatusActive  = "active"
	StatusPlanned = "planned"
)

// Stages is the gauntlet, in order.
func Stages() []Stage {
	return []Stage{
		{
			ID: "cold_deploy", Order: 1, Title: "Cold deploy", Status: StatusActive, Headline: true,
			Proves:     "The estate is real and buildable: the stock binary applies the unmodified configuration against the emulator, with no live block and no choudoufu involved. This is also the source of genuinely unmarked infrastructure for the next stage.",
			Oracle:     "This stage is the stock run. Its state file and its cloud are the baseline every later stage is compared to. A failure here is stock failing, not choudoufu, and is recorded as such.",
			Break:      "Not applicable; this stage has nothing of choudoufu's to break.",
			Substrates: map[string]string{SubstrateKind: "Stock applies the unmodified configuration against the kind cluster the script created for this run; the cluster's objects, read with kubectl, are the baseline."},
		},
		{
			ID: "migrate", Order: 2, Title: "Migrate", Status: StatusActive, Headline: true,
			Proves:     "`choudoufu live-import -approve` against the stock state file binds every instance: each state entry becomes a marker on the resource, a record, or an identity derived from the declaration, and the summary line reports zero skipped.",
			Oracle:     "The stock state file's instance list. Every address in it must be accounted for by name.",
			Break:      "Remove one instance from the expected count; the assertion on the summary line must fail.",
			Substrates: map[string]string{SubstrateKind: "The stock state file names each object by namespace and name, and a bound object carries the tofu-estate label the way a bound AWS resource carries its two tags; zero skipped means every entry was stamped or recorded."},
		},
		{
			ID: "test_plan", Order: 3, Title: "Replan from nothing", Status: StatusActive, Headline: true,
			Proves:     "With the state file deleted, `choudoufu live-plan` is empty, and a representative set of rendered identities equals what the AWS CLI reports for the same objects. An empty plan alone is not enough: a wrong identity can converge.",
			Oracle:     "Stock `plan` on the migrated state is also empty. Identity strings are compared by value against the CLI, which is the same answer stock's state would hold.",
			Break:      "Corrupt one expected identity string; stage 3 must fail on that string and nothing else.",
			Substrates: map[string]string{SubstrateKind: "Identities are NAMESPACE/NAME, compared by value with what kubectl reports."},
		},
		{
			ID: "test_apply", Order: 4, Title: "No-op apply", Status: StatusActive, Headline: true,
			Proves:     "Applying the empty plan changes nothing: the estate's tagged-object count before and after is identical.",
			Oracle:     "Stock `apply` of an empty plan is a no-op by definition; the object count is the comparison.",
			Break:      "Expect a different count; the assertion must fail.",
			Substrates: map[string]string{SubstrateKind: "The count is `kubectl get <kind> -A -l tofu-estate=<estate>` summed over the estate's kinds."},
		},
		{
			ID: "drift_reconverge", Order: 5, Title: "Drift and reconverge", Status: StatusActive, Headline: true,
			Proves:     "One live object is mutated out of band through the AWS CLI; the next plan proposes fixing exactly that object and nothing else, and apply reconverges it.",
			Oracle:     "Stock `plan` after the same mutation, with marker tags normalised out of both plans, proposes the same change.",
			Break:      "Mutate a second object as well; the single-object assertion must fail.",
			Substrates: map[string]string{SubstrateKind: "The mutation is a kubectl label or patch, never through the tool."},
		},
		{
			ID: "day2_rename", Order: 6, Title: "Rename", Status: StatusActive, Headline: true,
			Proves:     "Renaming a resource through a `moved` block and through `choudoufu live-mv` both produce zero churn: no destroy, no create, the marker rewritten in place.",
			Oracle:     "Stock with the same `moved` block plans zero churn. The two plans, normalised, are identical.",
			Break:      "Rename without the `moved` block; the plan must show a destroy and a create.",
			Substrates: map[string]string{SubstrateKind: "The moved-block half only: live-mv has no Kubernetes leg, because the object carries no address to rewrite (#1066). A rename without a moved block is zero churn here too, since the block name is not part of the object's identity, so the Break control is a rename of the object's own metadata.name instead, which is a replace and must plan a destroy and a create."},
		},
		{
			ID: "day2_remove", Order: 7, Title: "Remove a block", Status: StatusActive, Headline: true,
			Proves:     "Deleting a resource block destroys the object under the default policy, in an order the cloud accepts, including blocks for untaggable children whose parents stay.",
			Oracle:     "Stock with the same block removed plans the same destroys in a working order.",
			Break:      "Keep the block; no destroy may be proposed.",
			Substrates: map[string]string{SubstrateKind: "kubectl confirms the object is gone. With no address on the object, the sweep plans it at `<type>.orphan_<namespace>_<name>`, under the versioned type once no block declares the kind; a controller's copies, which the sweep excludes, are never proposed."},
		},
		{
			ID: "day2_count", Order: 8, Title: "Change count", Status: StatusActive, Headline: true,
			Proves:     "Scaling a `count` block down and back up destroys and creates only the instances stock would, and every surviving instance keeps its identity.",
			Oracle:     "Stock's plan for the same count change, normalised.",
			Break:      "Expect a different instance to be destroyed; the assertion must fail.",
			Substrates: map[string]string{SubstrateKind: "The instance that leaves the count is found by its label and planned at the sweep's orphan address, since the label carries no index; kubectl confirms it is the same object stock destroys and that the survivor is untouched. The instance that comes back is created at its declared address."},
		},
		{
			ID: "day2_replace", Order: 9, Title: "Replace with create_before_destroy", Status: StatusActive, Headline: true,
			Proves:     "A forced replacement under `create_before_destroy` creates the new object, destroys the old one, and the next plan is empty with no marker collision.",
			Oracle:     "Stock's replace of the same resource leaves the same single object.",
			Break:      "Skip the destroy half; the next plan must report a collision rather than proposing nothing.",
			Substrates: map[string]string{SubstrateKind: "n/a: A Kubernetes name is unique within its namespace, so nothing can be created before the object it replaces is destroyed; a forced replacement is destroy-then-create, which this stage does not measure."},
		},
		{
			ID: "day2_crash", Order: 10, Title: "Crash between create and destroy", Status: StatusActive, Headline: true, Tier1Gated: true,
			Proves:     "A replace interrupted after the create and before the destroy is recovered by the next plan without a human: the old object is destroyed, the new one is bound.",
			Oracle:     "Stock records the old object as deposed and destroys it on the next apply; the outcome after one more apply must be the same.",
			Break:      "Interrupt and then assert nothing is proposed; the assertion must fail.",
			Substrates: map[string]string{SubstrateKind: "n/a: The create-before-destroy window this stage interrupts does not exist on Kubernetes (see day2_replace)."},
		},
		{
			ID: "day2_teardown", Order: 11, Title: "Teardown", Status: StatusActive, Headline: true, Tier1Gated: true,
			Proves:     "`choudoufu apply -destroy` removes every object the estate owns in one apply, in an order the cloud accepts, and leaves nothing marked.",
			Oracle:     "Stock `apply -destroy` on the same estate leaves the same empty account.",
			Break:      "Leave one resource; the assertion that the estate is empty must fail.",
			Substrates: map[string]string{SubstrateKind: "An empty cluster is `kubectl get <kind> -A -l tofu-estate=<estate>` returning nothing for every kind."},
		},
		{
			ID: "plan_approval", Order: 12, Title: "Plan, review, apply", Status: StatusActive, Headline: true,
			Proves:     "`plan -out` followed by `apply <planfile>` applies when the world has not moved and refuses, naming the mismatch, when it has.",
			Oracle:     "Stock's planfile applies in the unchanged case; in the changed case choudoufu is stricter than stock by design, and the refusal is asserted, not compared.",
			Break:      "Apply the planfile after a mutation and expect success; the run must refuse.",
			Substrates: map[string]string{SubstrateKind: "The out-of-band move is a kubectl label."},
		},
		{
			ID: "greenfield", Order: 13, Title: "Greenfield apply", Status: StatusActive, Headline: true,
			Proves:     "Applying the same configuration from an empty account with choudoufu directly, no migration, produces the same objects stock's cold deploy produced, plus markers.",
			Oracle:     "The cloud after stock's cold deploy, compared object by object with marker tags normalised out.",
			Break:      "Drop one resource from the expected inventory; the comparison must fail.",
			Substrates: map[string]string{SubstrateKind: "Compared against the inventory recorded from stock's cold deploy earlier in the same run, object by object, with the label and the server-set fields normalised out; one cluster hosts both, in sequence."},
		},
		{
			ID: "strict", Order: 14, Title: "Strict profile", Status: StatusActive, Headline: false,
			Proves: "With every strict toggle on, the estate is refused for exactly the things the toggles name (secrets stored, markers unrepaired, and so on) with the documented message, and for nothing else. Tested and shown per estate; not part of the headline bars.",
			Oracle: "No stock equivalent. The toggle documentation is the oracle, and each toggle's fixture is the comparison.",
			Break:  "Turn a toggle off; its refusal must disappear and no other may appear.",
		},
	}
}

// ActiveStages is every stage whose Status is "active" - headline or not.
// This is what a crossing script actually runs and what the per-estate
// duration/verdict tables display; it says nothing about the two headline
// bars. For that, see HeadlineStages.
func ActiveStages() []Stage {
	var out []Stage
	for _, s := range Stages() {
		if s.Status == StatusActive {
			out = append(out, s)
		}
	}
	return out
}

// HeadlineStages is the subset of ActiveStages that counts toward the two
// headline bars (core/all estates clear) and toward what `next` selects as
// the unit to fix. isClear (artifact.go) and NextUnits (next.go) both key
// off this, not ActiveStages, so that flipping a non-headline stage's
// Status to active starts it running and being reported without moving
// either bar or ever being chosen as "next" work (#482).
func HeadlineStages() []Stage {
	var out []Stage
	for _, s := range ActiveStages() {
		if s.Headline {
			out = append(out, s)
		}
	}
	return out
}

// StageByID returns the stage with the given ID, or false.
func StageByID(id string) (Stage, bool) {
	for _, s := range Stages() {
		if s.ID == id {
			return s, true
		}
	}
	return Stage{}, false
}
