// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// TestContentDigest_Stable checks the digest is deterministic across repeat
// computation and across map iteration order (Go map ranges are randomized,
// so a digest that forgot to sort typeNames would flap between runs).
func TestContentDigest_Stable(t *testing.T) {
	schemas := loadTestdataSchemas(t)

	first := ContentDigest(schemas)
	for i := 0; i < 5; i++ {
		if got := ContentDigest(schemas); got != first {
			t.Fatalf("ContentDigest is not stable across repeat calls: run 1 got %s, run %d got %s", first, i+2, got)
		}
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Errorf("ContentDigest = %q, want a sha256: prefix", first)
	}
}

// TestContentDigest_MovesWithContentNotOrder checks the digest is sensitive
// to a single byte changing in one schema, and insensitive to the input
// map's construction order (maps have none, but this pins the intent).
func TestContentDigest_MovesWithContentNotOrder(t *testing.T) {
	schemas := loadTestdataSchemas(t)
	base := ContentDigest(schemas)

	// Mutate one schema's bytes; the digest must move.
	mutated := make(map[string][]byte, len(schemas))
	touched := false
	for name, data := range schemas {
		if !touched {
			mutated[name] = append(append([]byte(nil), data...), ' ')
			touched = true
			continue
		}
		mutated[name] = data
	}
	if got := ContentDigest(mutated); got == base {
		t.Error("ContentDigest did not move when one schema's bytes changed")
	}

	// Adding a type moves the digest; removing it moves the digest back.
	added := make(map[string][]byte, len(schemas)+1)
	for name, data := range schemas {
		added[name] = data
	}
	added["AWS::Test::NewType"] = []byte(`{"typeName":"AWS::Test::NewType"}`)
	if got := ContentDigest(added); got == base {
		t.Error("ContentDigest did not move when a type was added")
	}
}

// pinnedNamesFrom builds the map[string]bool ComputeDrift and AssertPinned
// take from a schema set's keys, standing in for a previously accepted
// roster in tests.
func pinnedNamesFrom(schemas map[string][]byte) map[string]bool {
	out := make(map[string]bool, len(schemas))
	for name := range schemas {
		out[name] = true
	}
	return out
}

func TestComputeDrift_NoDriftWhenDigestMatches(t *testing.T) {
	schemas := loadTestdataSchemas(t)
	pin := SpecPin{Digest: ContentDigest(schemas), Resources: len(schemas), Accepted: "2026-01-01"}

	if drift := ComputeDrift(schemas, pinnedNamesFrom(schemas), pin); drift != nil {
		t.Errorf("ComputeDrift with a matching digest: want nil, got %+v", drift)
	}
}

func TestComputeDrift_ResourceSetMoved(t *testing.T) {
	schemas := loadTestdataSchemas(t)
	pinnedNames := pinnedNamesFrom(schemas)
	pin := SpecPin{Digest: ContentDigest(schemas), Resources: len(schemas), Accepted: "2026-01-01"}

	// Remove one type and add a new one: the resource set moved both ways.
	var removedType string
	fresh := make(map[string][]byte, len(schemas))
	for name, data := range schemas {
		if removedType == "" {
			removedType = name
			continue
		}
		fresh[name] = data
	}
	fresh["AWS::Test::NewType"] = []byte(`{"typeName":"AWS::Test::NewType"}`)

	drift := ComputeDrift(fresh, pinnedNames, pin)
	if drift == nil {
		t.Fatal("ComputeDrift with an added and a removed type: want a drift, got nil")
	}
	if !drift.ResourceSetMoved() {
		t.Error("drift.ResourceSetMoved() = false, want true")
	}
	if len(drift.Added) != 1 || drift.Added[0] != "AWS::Test::NewType" {
		t.Errorf("drift.Added = %v, want [AWS::Test::NewType]", drift.Added)
	}
	if len(drift.Removed) != 1 || drift.Removed[0] != removedType {
		t.Errorf("drift.Removed = %v, want [%s]", drift.Removed, removedType)
	}
	if drift.Resources != len(fresh) {
		t.Errorf("drift.Resources = %d, want %d", drift.Resources, len(fresh))
	}
}

func TestComputeDrift_ByteOnly(t *testing.T) {
	schemas := loadTestdataSchemas(t)
	pinnedNames := pinnedNamesFrom(schemas)
	pin := SpecPin{Digest: ContentDigest(schemas), Resources: len(schemas), Accepted: "2026-01-01"}

	// Same type set, one schema's bytes edited: the resource set did not
	// move, but the digest must.
	mutated := make(map[string][]byte, len(schemas))
	touched := false
	for name, data := range schemas {
		if !touched {
			mutated[name] = append(append([]byte(nil), data...), ' ')
			touched = true
			continue
		}
		mutated[name] = data
	}

	drift := ComputeDrift(mutated, pinnedNames, pin)
	if drift == nil {
		t.Fatal("ComputeDrift after editing one schema's bytes: want a drift, got nil")
	}
	if drift.ResourceSetMoved() {
		t.Errorf("drift.ResourceSetMoved() = true for a byte-only edit, want false (Added=%v Removed=%v)", drift.Added, drift.Removed)
	}
}

// TestAssertPinned_RefusesResourceSetChange is the refusal half of issue
// #42's enforcement contract: a type added or removed is always fatal
// outside accept mode.
func TestAssertPinned_RefusesResourceSetChange(t *testing.T) {
	schemas := loadTestdataSchemas(t)
	pinnedNames := pinnedNamesFrom(schemas)
	pin := SpecPin{Digest: ContentDigest(schemas), Resources: len(schemas), Accepted: "2026-01-01"}

	fresh := make(map[string][]byte, len(schemas)+1)
	for name, data := range schemas {
		fresh[name] = data
	}
	fresh["AWS::Test::NewType"] = []byte(`{"typeName":"AWS::Test::NewType"}`)

	var warned []string
	err := AssertPinned(fresh, pinnedNames, pin, false, func(m string) { warned = append(warned, m) })
	if err == nil {
		t.Fatal("AssertPinned with a new type, accept=false: want a refusal, got nil")
	}
	if len(warned) != 0 {
		t.Errorf("AssertPinned refused but also warned %d time(s); the refusal is the only message", len(warned))
	}
	if !strings.Contains(err.Error(), "AWS::Test::NewType") {
		t.Errorf("refusal message does not name the added type: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "refuses") {
		t.Errorf("refusal message does not read as a refusal: %s", err.Error())
	}
}

// TestAssertPinned_WarnsByteOnlyDrift is the softened half (chant #1473):
// bytes moving with the type set unchanged warns and lets generation
// continue.
func TestAssertPinned_WarnsByteOnlyDrift(t *testing.T) {
	schemas := loadTestdataSchemas(t)
	pinnedNames := pinnedNamesFrom(schemas)
	pin := SpecPin{Digest: ContentDigest(schemas), Resources: len(schemas), Accepted: "2026-01-01"}

	mutated := make(map[string][]byte, len(schemas))
	touched := false
	for name, data := range schemas {
		if !touched {
			mutated[name] = append(append([]byte(nil), data...), ' ')
			touched = true
			continue
		}
		mutated[name] = data
	}

	var warned []string
	err := AssertPinned(mutated, pinnedNames, pin, false, func(m string) { warned = append(warned, m) })
	if err != nil {
		t.Fatalf("AssertPinned with byte-only drift, accept=false: want nil (a warning, not a refusal), got %v", err)
	}
	if len(warned) != 1 {
		t.Fatalf("AssertPinned with byte-only drift: want exactly one warning, got %d", len(warned))
	}
	if strings.Contains(warned[0], "refuses") {
		t.Errorf("byte-only drift warning reads as a refusal: %s", warned[0])
	}
}

// TestAssertPinned_NoDriftIsSilent checks the common case: a matching
// digest calls warn zero times and returns nil.
func TestAssertPinned_NoDriftIsSilent(t *testing.T) {
	schemas := loadTestdataSchemas(t)
	pin := SpecPin{Digest: ContentDigest(schemas), Resources: len(schemas), Accepted: "2026-01-01"}

	called := false
	err := AssertPinned(schemas, pinnedNamesFrom(schemas), pin, false, func(string) { called = true })
	if err != nil {
		t.Fatalf("AssertPinned with a matching digest: want nil, got %v", err)
	}
	if called {
		t.Error("AssertPinned called warn on a matching digest")
	}
}

// TestAssertPinned_AcceptNeverRefuses checks accept mode overrides the
// refusal for a resource-set change - the "accept-then-paste" loop issue
// #42 (via chant's driftMessage) documents as one command instead of two.
func TestAssertPinned_AcceptNeverRefuses(t *testing.T) {
	schemas := loadTestdataSchemas(t)
	pinnedNames := pinnedNamesFrom(schemas)
	pin := SpecPin{Digest: ContentDigest(schemas), Resources: len(schemas), Accepted: "2026-01-01"}

	fresh := make(map[string][]byte, len(schemas)+1)
	for name, data := range schemas {
		fresh[name] = data
	}
	fresh["AWS::Test::NewType"] = []byte(`{"typeName":"AWS::Test::NewType"}`)

	var warned []string
	err := AssertPinned(fresh, pinnedNames, pin, true, func(m string) { warned = append(warned, m) })
	if err != nil {
		t.Fatalf("AssertPinned with accept=true on a resource-set change: want nil, got %v", err)
	}
	if len(warned) != 1 {
		t.Fatalf("AssertPinned with accept=true: want exactly one warning naming the new pin, got %d", len(warned))
	}
	if !strings.Contains(warned[0], "AWS::Test::NewType") {
		t.Errorf("accept warning does not name the added type: %s", warned[0])
	}
}

// TestDriftMessage_NamesTheRefreshEdit checks the message tells a human
// exactly what to paste and where, since that instruction is the whole
// point of a refusal that isn't just "digest mismatch".
func TestDriftMessage_NamesTheRefreshEdit(t *testing.T) {
	drift := &Drift{Digest: "sha256:abc123", Resources: 42, Added: []string{"AWS::Test::New"}}
	pin := SpecPin{Digest: "sha256:old", Resources: 41, Accepted: "2026-01-01"}

	msg := DriftMessage(drift, pin, true)
	for _, want := range []string{"sha256:abc123", "42", "pinned_spec.go", "pinned-types.json", AcceptEnv} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal message missing %q:\n%s", want, msg)
		}
	}
}
