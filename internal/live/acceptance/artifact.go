// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// artifactRel is the generated artifact's repo-relative path.
const artifactRel = "live/cohort-acceptance.json"

// Phase names how far a cohort got. The values are ordered: each phase
// requires every earlier one.
type Phase string

const (
	// PhaseInit: terraform/tofu init failed - a harness or registry
	// problem, not a fixture verdict.
	PhaseInit Phase = "init"
	// PhaseApply: the stock apply against the emulator did not create
	// every resource. The fixture, the provider or the emulator refused;
	// FailedResources carries what the apply output named.
	PhaseApply Phase = "apply"
	// PhaseReplan: live-plan errored after the state was deleted.
	PhaseReplan Phase = "replan"
	// PhaseEmpty: live-plan exited cleanly but the run could not confirm
	// an empty plan - it proposed changes (FailedResources carries the
	// addresses), or printed output this harness does not recognize, which
	// is recorded as a failure to assert rather than presumed empty.
	PhaseEmpty Phase = "empty"
	// PhasePass: applied, state deleted, replanned empty.
	PhasePass Phase = "pass"
)

// CohortResult is one cohort's verdict.
type CohortResult struct {
	Name string `json:"name"`
	// Status is "pass" or "fail". "unsupported" is reserved for a cohort a
	// capability probe rules out before any apply; #99's probe reported
	// every listable admitted type implemented, so no cohort carries it
	// today and the field earns its place only when the emulator loses
	// something.
	Status string `json:"status"`
	// Phase is PhasePass, or the first phase that failed.
	Phase Phase `json:"phase"`
	// Resources is the number of resource blocks the cohort declares.
	Resources int `json:"resources"`
	// FailedResources names the addresses the failing phase reported, when
	// it reported any: apply errors name the resource that refused,
	// replan-not-empty names the addresses with proposed changes.
	FailedResources []string `json:"failed_resources,omitempty"`
	// Detail is the first error line of the failing phase, enough to find
	// the full output in the test log without pasting it all here.
	Detail string `json:"detail,omitempty"`
	// TimedOut is set when the failing phase was killed at its deadline
	// rather than exiting - the API Gateway availability waiter against
	// floci is the known shape.
	TimedOut bool `json:"timed_out,omitempty"`
	// LastRun is this row's own provenance: the commit and emulator digest
	// this cohort was actually measured against, the same per-row idiom
	// live/gauntlet.json's estates carry (last_run.commit since #413,
	// last_run.emulator since #414/f27f19d443). See LastRun's doc comment
	// for why this lives on the row and not only at the artifact's
	// top-level Image/GeneratedBy.
	LastRun *LastRun `json:"last_run,omitempty"`
}

// LastRun records the run that produced one cohort's verdict.
//
// TestCohortAcceptance writes the whole artifact in one pass today - every
// row in one test run, all-or-nothing (see enforceRatchet's refusal to
// write a partial artifact) - so Commit/Emulator are identical across every
// row in a given write. That is an artifact of how the runner happens to
// work today, not a property this schema may assume: #414 (f27f19d443)
// found the same "true for one instant, false after an incremental re-run"
// trap one layer up, in live/gauntlet.json, where per-estate runs made the
// board-wide claim silently stale. A per-row stamp costs nothing when runs
// are monolithic and is the only thing that stays honest if a future change
// ever runs cohorts incrementally (a `-run` subset, a per-cohort retry) the
// way gauntlet.json's estates already do - so a board-wide claim must always
// be able to say it derived from these rows, never from the top-level
// Image/GeneratedBy fields, which describe the checkout's CURRENT
// configuration (the pin the NEXT run will use), not evidence about what
// any past row was actually measured against.
type LastRun struct {
	Commit   string `json:"commit"`
	Date     string `json:"date"`
	Emulator string `json:"emulator"`
}

// IsStale reports whether r's last recorded run measured against a
// different emulator image than currentEmulator - the same fact
// tools/gauntlet.IsStale computes for live/gauntlet.json's estate rows. A
// row with no LastRun at all (r.LastRun == nil) is a different fact from
// "measured, but against a superseded pin" and callers should check for it
// separately; this function only answers the second question.
//
// Nothing in this package queues stale cohorts as work yet - unlike
// tools/gauntlet's `next`, there is no `cohort next` command, and wiring one
// is left as a follow-up (issue #433's PR says so explicitly) rather than
// done blind alongside the schema change. This is the primitive a future
// "cohort next" would need, published now so that follow-up does not have
// to invent the field names or the comparison a second time.
func IsStale(r CohortResult, currentEmulator string) bool {
	return r.LastRun != nil && r.LastRun.Emulator != currentEmulator
}

// Artifact is live/cohort-acceptance.json's whole shape.
type Artifact struct {
	GeneratedBy string `json:"generated_by"`
	// Image is the emulator image this run's checkout is pinned to,
	// digest included - CONFIGURATION, not a claim about what any one row
	// was measured against. Read per-row LastRun.Emulator for evidence (see
	// LastRun's doc comment).
	Image string `json:"image"`
	// Provider is the AWS provider release the fixtures pin.
	Provider string `json:"provider"`
	Totals   struct {
		Cohorts int `json:"cohorts"`
		Pass    int `json:"pass"`
		Fail    int `json:"fail"`
	} `json:"totals"`
	Cohorts []CohortResult `json:"cohorts"`
}

// buildArtifact folds per-cohort results into the artifact, sorted by name,
// and stamps each row's own LastRun from commit/image - the values this
// call's caller read at the moment it actually ran (TestCohortAcceptance:
// flocitest.HeadCommit and flocitest.Image), never recomputed later at
// render time. date is the single instant this whole (currently monolithic)
// run happened; every row shares it for the same reason every row shares
// commit and image today - see LastRun's doc comment on why that is not
// assumed to stay true forever.
func buildArtifact(image, provider, commit, date string, results []CohortResult) Artifact {
	art := Artifact{
		GeneratedBy: "TF_FLOCI_TEST=1 TF_FLOCI_ACCEPTANCE_ARTIFACT=1 go test ./internal/live/acceptance -run TestCohortAcceptance",
		Image:       image,
		Provider:    provider,
	}
	for _, r := range results {
		r.LastRun = &LastRun{Commit: commit, Date: date, Emulator: image}
		art.Cohorts = append(art.Cohorts, r)
	}
	sort.Slice(art.Cohorts, func(i, j int) bool { return art.Cohorts[i].Name < art.Cohorts[j].Name })
	for _, r := range art.Cohorts {
		art.Totals.Cohorts++
		if r.Status == "pass" {
			art.Totals.Pass++
		} else {
			art.Totals.Fail++
		}
	}
	return art
}

// writeArtifact writes the artifact to path with a trailing newline, the
// same framing every other generated JSON artifact in live/ uses.
func writeArtifact(path string, art Artifact) error {
	data, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644) //nolint:gosec // a committed artifact, not a secret
}

// readArtifact reads a previously committed artifact; a missing file is not
// an error, it is the first run.
func readArtifact(path string) (Artifact, bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if os.IsNotExist(err) {
		return Artifact{}, false, nil
	}
	if err != nil {
		return Artifact{}, false, err
	}
	var art Artifact
	if err := json.Unmarshal(data, &art); err != nil {
		return Artifact{}, false, fmt.Errorf("decoding %s: %w", path, err)
	}
	return art, true, nil
}
