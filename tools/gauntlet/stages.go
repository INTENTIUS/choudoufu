// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

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
// "active", and flipping it is a deliberate change that lowers the headline
// number until estates catch up.
type Stage struct {
	ID     string `json:"id"`
	Order  int    `json:"order"`
	Title  string `json:"title"`
	Status string `json:"status"` // "active" or "planned"
	Proves string `json:"proves"`
	Oracle string `json:"oracle"`
	Break  string `json:"break"`
}

const (
	StatusActive  = "active"
	StatusPlanned = "planned"
)

// Stages is the gauntlet, in order.
func Stages() []Stage {
	return []Stage{
		{
			ID: "cold_deploy", Order: 1, Title: "Cold deploy", Status: StatusActive,
			Proves: "The estate is real and buildable: the stock binary applies the unmodified configuration against the emulator, with no live block and no choudoufu involved. This is also the source of genuinely unmarked infrastructure for the next stage.",
			Oracle: "This stage is the stock run. Its state file and its cloud are the baseline every later stage is compared to. A failure here is stock failing, not choudoufu, and is recorded as such.",
			Break:  "Not applicable; this stage has nothing of choudoufu's to break.",
		},
		{
			ID: "migrate", Order: 2, Title: "Migrate", Status: StatusActive,
			Proves: "`choudoufu live-import -approve` against the stock state file binds every instance: each state entry becomes a marker on the resource, a record, or an identity derived from the declaration, and the summary line reports zero skipped.",
			Oracle: "The stock state file's instance list. Every address in it must be accounted for by name.",
			Break:  "Remove one instance from the expected count; the assertion on the summary line must fail.",
		},
		{
			ID: "test_plan", Order: 3, Title: "Replan from nothing", Status: StatusActive,
			Proves: "With the state file deleted, `choudoufu live-plan` is empty, and a representative set of rendered identities equals what the AWS CLI reports for the same objects. An empty plan alone is not enough: a wrong identity can converge.",
			Oracle: "Stock `plan` on the migrated state is also empty. Identity strings are compared by value against the CLI, which is the same answer stock's state would hold.",
			Break:  "Corrupt one expected identity string; stage 3 must fail on that string and nothing else.",
		},
		{
			ID: "test_apply", Order: 4, Title: "No-op apply", Status: StatusActive,
			Proves: "Applying the empty plan changes nothing: the estate's tagged-object count before and after is identical.",
			Oracle: "Stock `apply` of an empty plan is a no-op by definition; the object count is the comparison.",
			Break:  "Expect a different count; the assertion must fail.",
		},
		{
			ID: "drift_reconverge", Order: 5, Title: "Drift and reconverge", Status: StatusActive,
			Proves: "One live object is mutated out of band through the AWS CLI; the next plan proposes fixing exactly that object and nothing else, and apply reconverges it.",
			Oracle: "Stock `plan` after the same mutation, with marker tags normalised out of both plans, proposes the same change.",
			Break:  "Mutate a second object as well; the single-object assertion must fail.",
		},
		{
			ID: "day2_rename", Order: 6, Title: "Rename", Status: StatusPlanned,
			Proves: "Renaming a resource through a `moved` block and through `choudoufu live-mv` both produce zero churn: no destroy, no create, the marker rewritten in place.",
			Oracle: "Stock with the same `moved` block plans zero churn. The two plans, normalised, are identical.",
			Break:  "Rename without the `moved` block; the plan must show a destroy and a create.",
		},
		{
			ID: "day2_remove", Order: 7, Title: "Remove a block", Status: StatusPlanned,
			Proves: "Deleting a resource block destroys the object under the default policy, in an order the cloud accepts, including blocks for untaggable children whose parents stay.",
			Oracle: "Stock with the same block removed plans the same destroys in a working order.",
			Break:  "Keep the block; no destroy may be proposed.",
		},
		{
			ID: "day2_count", Order: 8, Title: "Change count", Status: StatusPlanned,
			Proves: "Scaling a `count` block down and back up destroys and creates only the instances stock would, and every surviving instance keeps its identity.",
			Oracle: "Stock's plan for the same count change, normalised.",
			Break:  "Expect a different instance to be destroyed; the assertion must fail.",
		},
		{
			ID: "day2_replace", Order: 9, Title: "Replace with create_before_destroy", Status: StatusPlanned,
			Proves: "A forced replacement under `create_before_destroy` creates the new object, destroys the old one, and the next plan is empty with no marker collision.",
			Oracle: "Stock's replace of the same resource leaves the same single object.",
			Break:  "Skip the destroy half; the next plan must report a collision rather than proposing nothing.",
		},
		{
			ID: "day2_crash", Order: 10, Title: "Crash between create and destroy", Status: StatusPlanned,
			Proves: "A replace interrupted after the create and before the destroy is recovered by the next plan without a human: the old object is destroyed, the new one is bound.",
			Oracle: "Stock records the old object as deposed and destroys it on the next apply; the outcome after one more apply must be the same.",
			Break:  "Interrupt and then assert nothing is proposed; the assertion must fail.",
		},
		{
			ID: "day2_teardown", Order: 11, Title: "Teardown", Status: StatusPlanned,
			Proves: "`choudoufu apply -destroy` removes every object the estate owns in one apply, in an order the cloud accepts, and leaves nothing marked.",
			Oracle: "Stock `apply -destroy` on the same estate leaves the same empty account.",
			Break:  "Leave one resource; the assertion that the estate is empty must fail.",
		},
		{
			ID: "plan_approval", Order: 12, Title: "Plan, review, apply", Status: StatusPlanned,
			Proves: "`plan -out` followed by `apply <planfile>` applies when the world has not moved and refuses, naming the mismatch, when it has.",
			Oracle: "Stock's planfile applies in the unchanged case; in the changed case choudoufu is stricter than stock by design, and the refusal is asserted, not compared.",
			Break:  "Apply the planfile after a mutation and expect success; the run must refuse.",
		},
		{
			ID: "greenfield", Order: 13, Title: "Greenfield apply", Status: StatusPlanned,
			Proves: "Applying the same configuration from an empty account with choudoufu directly, no migration, produces the same objects stock's cold deploy produced, plus markers.",
			Oracle: "The cloud after stock's cold deploy, compared object by object with marker tags normalised out.",
			Break:  "Drop one resource from the expected inventory; the comparison must fail.",
		},
		{
			ID: "strict", Order: 14, Title: "Strict profile", Status: StatusPlanned,
			Proves: "With every strict toggle on, the estate is refused for exactly the things the toggles name (secrets stored, markers unrepaired, and so on) with the documented message, and for nothing else. Tested and shown per estate; not part of the headline bars.",
			Oracle: "No stock equivalent. The toggle documentation is the oracle, and each toggle's fixture is the comparison.",
			Break:  "Turn a toggle off; its refusal must disappear and no other may appear.",
		},
	}
}

// ActiveStages is the subset an estate must pass to count as clear.
func ActiveStages() []Stage {
	var out []Stage
	for _, s := range Stages() {
		if s.Status == StatusActive {
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
