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
}

// Artifact is live/cohort-acceptance.json's whole shape.
type Artifact struct {
	GeneratedBy string `json:"generated_by"`
	// Image is the emulator image the run used, digest included.
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

// buildArtifact folds per-cohort results into the artifact, sorted by name.
func buildArtifact(image, provider string, results []CohortResult) Artifact {
	art := Artifact{
		GeneratedBy: "TF_FLOCI_TEST=1 TF_FLOCI_ACCEPTANCE_ARTIFACT=1 go test ./internal/live/acceptance -run TestCohortAcceptance",
		Image:       image,
		Provider:    provider,
	}
	art.Cohorts = append(art.Cohorts, results...)
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
