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
// #522's ruling) rather than one hand-written section per estate. #491 and
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
func Stages() []Stage { return stagesSource() }

// stagesSource is where [Stages] reads the registry. A test swaps it to
// exercise a mechanism no registered stage uses today: since #1641 no
// stage is n/a on kind, and the n/a rules still have to hold for the next
// one that is.
var stagesSource = registeredStages

func registeredStages() []Stage {
	stages := []Stage{
		{
			ID: "cold_deploy", Order: 1, Title: "Cold deploy", Status: StatusActive, Headline: true,
			Proves:     "The estate is real and buildable: the stock binary applies the unmodified configuration against the emulator, with no live block and no choudoufu involved. This is also the source of genuinely unmarked infrastructure for the next stage. A configuration that stock itself cannot plan in one pass may declare a pre-apply (`pre_apply` in the manifest, #1173): the named addresses are applied with `-target` first, identically on every side, and the verdict line says how many and where they are declared while the run reports the list itself for the runner to check address by address.",
			Oracle:     "This stage is the stock run. Its state file and its cloud are the baseline every later stage is compared to. A failure here is stock failing, not choudoufu, and is recorded as such. A declared pre-apply is performed by the stock oracle too, from the same list - a crossing where one side got a targeted first apply and the other did not would not be comparing like with like.",
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
			Substrates: map[string]string{SubstrateKind: "The moved-block half only: live-mv also has a Kubernetes leg since #1639, not exercised by this stage. A bare rename without a moved block plans the same one in-place change to the address annotation, since the block name is not part of the object's identity, so the Break control is a rename of the object's own metadata.name instead, which is a genuine identity change and must plan a destroy and a create."},
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
			Substrates: map[string]string{SubstrateKind: "A Kubernetes name is unique within its namespace, so the replacement create_before_destroy exists for here is a rename: a content-hashed name such as `cfg-${sha}` changes, and the Deployment reading the object rolls onto the new one before the old one goes. The old object carries the block's address annotation, the sweep binds it to the block (#1640), and the plan is the replace stock plans (`must be replaced`, create first). At `-parallelism=1` the apply log shows the new object's create complete before the old object's destroy starts, kubectl confirms only the new object remains, and the next plan is empty. A replacement that keeps its name is destroy-then-create on either tool and is not what this stage measures (#1541). The Break control recreates the old object, carrying the block's annotation, after the apply: the next plan must propose destroying it rather than nothing."},
		},
		{
			ID: "day2_crash", Order: 10, Title: "Crash mid-apply", Status: StatusActive, Headline: true, Tier1Gated: true,
			Proves:     "A replace interrupted after the create and before the destroy is recovered by the next plan without a human: the old object is destroyed, the new one is bound.",
			Oracle:     "Stock records the old object as deposed and destroys it on the next apply; the outcome after one more apply must be the same.",
			Break:      "Interrupt and then assert nothing is proposed; the assertion must fail.",
			Substrates: map[string]string{SubstrateKind: "The emulator's window exists here too, and this stage interrupts it (#1768, after #1683). A create_before_destroy rename (day2_replace, #1541) creates the new object, carrying the block's address annotation, before destroying the old one; the engine's own interrupt at -parallelism=1 kills the apply the instant the new object's create commits, leaving both objects annotated with one address and the record holding the old one as the address's deposed object. The oracle is the outcome, not the plan's wording: stock plans a deposed-object destroy, while with the same configuration the rerun here plans the old object's destroy at its orphan address, because the new object is at the declared, listed key. What must agree is the end state after one more apply, the one stock's replace leaves: exactly the new object, the old one gone, the record's deposed entry cleared, and an empty replan. Both paths are run: the same configuration (the orphan destroy), and a name read from another block's attribute (#1539's shape), where only the deposed record settles which claimant is the old object (#1683) and the plan reads stock's deposed-object destroy. Both use kubernetes_config_map, whose record renders an identity; a kubernetes_config_map_v1 record does not, and that shape still ends in the two-claimant collision. The Break line covers both windows. The stage also interrupts an apply that creates several objects: a real SIGTERM lands between one object's create committing and the next object's ever being dispatched, and the next plan must propose exactly the remainder, binding the object already created by its tofu-estate label and its namespace and name rather than creating it a second time or sweeping it as an orphan. The oracle is stock's own plan on the oracle cluster from the same position, reached by applying the first object alone. A cross-estate live-mv has no such window: it makes exactly one governed write, the label patch itself, and re-keys no record (internal/live/mv/mv.go returns before propagateModuleRename for a cross-estate move, because the record it would move lives in the estate being left). A same-estate rename is no longer write-free (#1639): on a metadata-block object it rewrites the address annotation in one provider write, which has no window of its own, but on a kubernetes_manifest object it is two requests, the merge patch and then the ownership hand-off (#1704), and a kill between them is a window this stage does not interrupt either; its rerun does not recover it today (#1764)."},
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
	for i := range stages {
		note, ok := flociEKSNotes[stages[i].ID]
		if !ok {
			continue
		}
		if stages[i].Substrates == nil {
			stages[i].Substrates = map[string]string{}
		}
		stages[i].Substrates[SubstrateFlociEKS] = note
	}
	return stages
}

// flociEKSNotes is how each stage reads on SubstrateFlociEKS (#1113): an
// AWS-lane estate whose configuration also manages the cluster its own
// aws_eks_cluster creates, run against floci's EKS real mode. Kept beside
// the registry rather than inside each literal so the fourteen notes read as
// one account of one substrate; TestEveryStageHasAFlociEKSNote holds that no
// stage is left without one. What k3s cannot stand in for is said once, on
// cold_deploy, and is the reason the estate's real-AWS live-cert cycle
// exists.
var flociEKSNotes = map[string]string{
	"cold_deploy":      "Stock applies the unmodified configuration against the emulator, and floci's EKS real mode starts a k3s container for each aws_eks_cluster. The configuration's own provider \"kubernetes\" block reaches that cluster exactly as written, with no delta repointing it, so every later stage measures the block under test. The AWS resources, and the cluster's objects read with kubectl inside the k3s container, are the baseline. k3s stands in for the control plane and nothing behind it: IRSA, EKS Pod Identity, the VPC CNI, EBS CSI volume claims, access-entry authorization and managed add-ons are not emulated, and are measured only by the estate's real-AWS live-cert cycle.",
	"migrate":          "One state file covers both legs: an AWS entry is bound as on floci, a Kubernetes entry as on kind, its tofu-estate label read with kubectl inside the k3s container. The provider block reads the cluster's endpoint and credential from the estate's own resources, so this stage also measures the provider-configuration fixpoint (#1113): a cluster leg reported unreachable while the cluster exists is a failure, never a skip.",
	"test_plan":        "AWS identities compare as on floci, Kubernetes identities as NAMESPACE/NAME against kubectl in the k3s container. The kubernetes provider is configured from the cluster this run reads live, so an empty plan is also evidence that the provider block was answered as written.",
	"test_apply":       "The count sums both legs: the AWS markers as on floci, plus `kubectl get <kind> -A -l tofu-estate=<estate>` inside the k3s container.",
	"drift_reconverge": "The mutation is made on either leg out of band: through the emulator's API on the AWS side, or as a kubectl label or patch inside the k3s container, never through the tool.",
	"day2_rename":      "Read on the leg the renamed block lives on, as that leg's own substrate reads it.",
	"day2_remove":      "The removed block's object is confirmed gone on its own leg: through the emulator's API for AWS, with kubectl in the k3s container for Kubernetes. Objects EKS itself writes (CoreDNS, kube-proxy, add-on Deployments) carry no tofu-estate label and are never proposed; one the configuration declares, such as aws-auth, is the operator's, as it is in stock.",
	"day2_count":       "Read on the leg the counted block lives on, as that leg's own substrate reads it.",
	"day2_replace":     "Read on the leg the replaced block lives on, as that leg's own substrate reads it.",
	"day2_crash":       "Read on the leg the interrupted object lives on, as that leg's own substrate reads it. An apply spanning both legs is interrupted at whichever create commits first.",
	"day2_teardown":    "Both legs end empty: the AWS listing as on floci, and kubectl in the k3s container returning nothing for every kind the estate declared. The cluster leg is destroyed before the cluster it lives on, the order the configuration's own dependency graph gives, so no load balancer or network interface outlives the cluster.",
	"plan_approval":    "As on floci; the saved plan carries both legs, and the apply of it configures the kubernetes provider from the same cluster the plan read.",
	"greenfield":       "Both legs are compared against stock's cold deploy: AWS objects as on floci, cluster objects as on kind. A greenfield plan starts with no cluster, so the kubernetes provider's sweep reads that leg as empty by construction, stock's order, and the apply configures the provider once the cluster exists.",
	"strict":           "As on floci; the toggles apply to both legs alike.",
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
