// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProviderVersionsReadsThePin: providerVersions reads
// live/oracle-versions.json's aws_provider_version and
// kubernetes_provider_version fields (#1253), the same graceful-empty
// pattern oracleVersions and emulatorPin already use.
func TestProviderVersionsReadsThePin(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	pin := `{"aws_provider_version": "6.63.0", "kubernetes_provider_version": "3.2.1"}`
	if err := os.WriteFile(filepath.Join(root, "live", "oracle-versions.json"), []byte(pin), 0o644); err != nil {
		t.Fatal(err)
	}
	got := providerVersions(root)
	want := ProviderVersions{AWS: "6.63.0", Kubernetes: "3.2.1"}
	if got != want {
		t.Errorf("providerVersions = %+v, want %+v", got, want)
	}
}

// TestProviderVersionsMissingPinIsZeroValue: a missing pin file reads as
// the zero value, never an error and never a guess.
func TestProviderVersionsMissingPinIsZeroValue(t *testing.T) {
	root := t.TempDir()
	if got := providerVersions(root); got != (ProviderVersions{}) {
		t.Errorf("providerVersions with no pin file = %+v, want zero value", got)
	}
}

// TestRebuildSetsArtifactProviders: a.Providers is a plain copy of
// Rebuild's providers argument, refreshed on every call - the same
// "configuration for the next run" role a.Emulator and a.Oracle already
// play.
func TestRebuildSetsArtifactProviders(t *testing.T) {
	m := &Manifest{Estates: []Estate{{Name: "a", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"}}}
	a := &Artifact{}
	a.Rebuild(m, nil, "img", OracleVersions{}, ProviderVersions{AWS: "6.63.0", Kubernetes: "3.2.1"})
	if a.Providers != (ProviderVersions{AWS: "6.63.0", Kubernetes: "3.2.1"}) {
		t.Errorf("a.Providers = %+v after Rebuild, want the passed-in value", a.Providers)
	}
	a.Rebuild(m, nil, "img", OracleVersions{}, ProviderVersions{AWS: "6.64.0", Kubernetes: "3.2.1"})
	if a.Providers != (ProviderVersions{AWS: "6.64.0", Kubernetes: "3.2.1"}) {
		t.Errorf("a.Providers = %+v after a second Rebuild, want the refreshed value (not carried forward)", a.Providers)
	}
}

// TestIsProviderStale mirrors TestIsStale's own shape (which does not
// exist as a standalone test but IsStale's doc comment states the rule):
// a floci-substrate row is compared against the AWS pin, a kind-substrate
// row against the Kubernetes pin, and a row with no last_run is never
// "stale" by this definition.
func TestIsProviderStale(t *testing.T) {
	pins := ProviderVersions{AWS: "6.63.0", Kubernetes: "3.2.1"}

	noRun := EstateResult{Name: "x"}
	if IsProviderStale(noRun, pins) {
		t.Errorf("a row with no last_run must not be reported provider-stale")
	}

	freshAWS := EstateResult{Name: "aws", LastRun: &LastRun{AWSProviderVersion: "6.63.0"}}
	if IsProviderStale(freshAWS, pins) {
		t.Errorf("an AWS row matching the current pin must not be stale")
	}
	staleAWS := EstateResult{Name: "aws2", LastRun: &LastRun{AWSProviderVersion: "6.58.0"}}
	if !IsProviderStale(staleAWS, pins) {
		t.Errorf("an AWS row measured against a superseded release must be stale")
	}
	unrecordedAWS := EstateResult{Name: "aws3", LastRun: &LastRun{}}
	if !IsProviderStale(unrecordedAWS, pins) {
		t.Errorf("an AWS row with no recorded provider version must read stale, the same 'unknown reads as stale' rule IsStale documents")
	}

	freshK8s := EstateResult{Name: "k8s", Substrate: SubstrateKind, LastRun: &LastRun{KubernetesProviderVersion: "3.2.1"}}
	if IsProviderStale(freshK8s, pins) {
		t.Errorf("a Kubernetes row matching the current pin must not be stale")
	}
	staleK8s := EstateResult{Name: "k8s2", Substrate: SubstrateKind, LastRun: &LastRun{KubernetesProviderVersion: "3.1.0"}}
	if !IsProviderStale(staleK8s, pins) {
		t.Errorf("a Kubernetes row measured against a superseded release must be stale")
	}
}

// TestNextSurfacesStaleProviderPinEstates: a clear estate whose last
// recorded provider version no longer matches the current pin is surfaced
// as trailing work by NextUnits, the same treatment a stale emulator pin
// already gets (#1253).
func TestNextSurfacesStaleProviderPinEstates(t *testing.T) {
	active := HeadlineStages()
	if len(active) == 0 {
		t.Skip("no headline stages")
	}
	m := &Manifest{Estates: []Estate{
		{Name: "c-fresh", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "c-stale-provider", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
	}}
	a := &Artifact{}
	a.Rebuild(m, nil, "pin", OracleVersions{}, ProviderVersions{AWS: "6.63.0"})

	allPass := map[string]string{}
	for _, s := range active {
		allPass[s.ID] = VerdictPass
	}
	setStages := func(name string, verdicts map[string]string) {
		r, _ := a.Result(name)
		for k, v := range verdicts {
			r.Stages[k] = v
		}
		a.SetResult(r)
	}
	setStages("c-fresh", allPass)
	setStages("c-stale-provider", allPass)

	setLastRun := func(name, emulator, aws string) {
		r, _ := a.Result(name)
		r.LastRun = &LastRun{Commit: "x", Date: "2020-01-01T00:00:00Z", Emulator: emulator, AWSProviderVersion: aws, ExitCode: 0}
		a.SetResult(r)
	}
	setLastRun("c-fresh", "pin", "6.63.0")
	setLastRun("c-stale-provider", "pin", "6.58.0")
	a.Rebuild(m, nil, "pin", OracleVersions{}, ProviderVersions{AWS: "6.63.0"})

	units := NextUnits(a, "all", "")
	var ids []string
	for _, u := range units {
		ids = append(ids, u.ID)
	}
	for _, id := range ids {
		if strings.HasPrefix(id, "c-fresh/") {
			t.Errorf("c-fresh matches both the emulator and provider pin; it must not appear as work, got %v", ids)
		}
	}
	found := false
	for _, id := range ids {
		if id == "c-stale-provider/stale_pin" {
			found = true
		}
	}
	if !found {
		t.Errorf("c-stale-provider's recorded AWS provider version no longer matches the pin; expected a stale_pin unit, got %v", ids)
	}
}

// TestBoardEstateProviderNote: the estate's provider-provenance note
// (#1253, beside the existing emulator and oracle ones) is silent for a
// row that predates it, and otherwise reports a match or a **Stale** note
// against a.Providers, the same shape oracleNote already uses.
func TestBoardEstateProviderNote(t *testing.T) {
	a := &Artifact{Providers: ProviderVersions{AWS: "6.63.0"}, Stages: Stages()}
	r := EstateResult{Name: "x", Protocol: ProtocolGauntlet, Stages: map[string]string{}}

	if note := boardEstate(r, a, ScriptStaleness{}, "").ProviderNote; note != "" {
		t.Errorf("no LastRun at all: note should be empty, got %q", note)
	}

	r.LastRun = &LastRun{Commit: "c", Date: "d", AWSProviderVersion: "6.63.0"}
	if note := boardEstate(r, a, ScriptStaleness{}, "").ProviderNote; !strings.Contains(note, "matches the current pin") {
		t.Errorf("expected a matching-provider note; got %q", note)
	}

	r.LastRun.AWSProviderVersion = "6.58.0"
	note := boardEstate(r, a, ScriptStaleness{}, "").ProviderNote
	if !strings.Contains(note, "6.58.0") || !strings.Contains(note, "**Stale**") || !strings.Contains(note, "6.63.0") {
		t.Errorf("expected a stale-provider note naming both versions; got %q", note)
	}
}
