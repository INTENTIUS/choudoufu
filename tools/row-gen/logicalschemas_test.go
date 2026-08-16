// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestLogicalSchemasArtifactCoversEverySource is the drift check on the
// committed artifact: it is a measurement of the providers named in
// [logicalProviderSources], and a hand edit to either half that the other
// does not follow makes -emit derive rows from evidence nobody acquired.
//
// The external source it consults is the source list itself - the pinned
// versions this generator would re-acquire - rather than anything derived
// from the artifact.
func TestLogicalSchemasArtifactCoversEverySource(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	bySource := make(map[string]logicalProviderSchemas, len(art.Providers))
	for _, p := range art.Providers {
		if _, dup := bySource[p.Source]; dup {
			t.Errorf("%s carries two entries for %s", logicalSchemasJSONRel, p.Source)
		}
		bySource[p.Source] = p
	}
	if len(bySource) != len(logicalProviderSources) {
		t.Errorf("%s carries %d provider(s), but logicalProviderSources names %d",
			logicalSchemasJSONRel, len(bySource), len(logicalProviderSources))
	}

	for _, src := range logicalProviderSources {
		wantSource, wantVersion := src.Source, src.Version
		if src.Builtin {
			wantSource, wantVersion = builtinProviderSource, choudoufuVersionPlaceholder
		}
		got, ok := bySource[wantSource]
		if !ok {
			t.Errorf("%s has no entry for %s; re-run `go run ./tools/row-gen -logical-schemas`", logicalSchemasJSONRel, wantSource)
			continue
		}
		if got.Version != wantVersion {
			t.Errorf("%s records %s at %s, but logicalProviderSources pins %s - re-acquire rather than editing the artifact",
				logicalSchemasJSONRel, wantSource, got.Version, wantVersion)
		}
		if got.StoreOnly != src.StoreOnly {
			t.Errorf("%s records store_only=%v for %s, but logicalProviderSources says %v",
				logicalSchemasJSONRel, got.StoreOnly, wantSource, src.StoreOnly)
		}
		if got.StoreOnlyEvidence != src.StoreOnlyEvidence {
			t.Errorf("%s's store_only_evidence for %s does not match logicalProviderSources'", logicalSchemasJSONRel, wantSource)
		}
		if len(got.Types) == 0 {
			t.Errorf("%s records no resource types for %s; an empty provider silently removes rows", logicalSchemasJSONRel, wantSource)
		}
	}
}

// TestRecordBackedDerivationReproducesEveryCommittedRow checks the
// derivation against the table it now writes: every RecordBacked row in the
// committed [identity.DefaultTable] must come back out of the rule, and the
// rule must produce nothing the table lacks.
//
// It is deliberately a two-way equality rather than a subset check. A
// one-way check would pass a rule that had quietly stopped deriving a row
// (the table would still carry it from the previous run, and -emit would
// drop it on the next); recordBackedRows refuses that case outright, and
// this is the same assertion stated where a reader looks for it.
func TestRecordBackedDerivationReproducesEveryCommittedRow(t *testing.T) {
	derived := recordBackedTypes(loadLogicalSchemasForTest(t))
	sort.Strings(derived)

	var committed []string
	for typeName, row := range identity.DefaultTable {
		if row.RecordBacked {
			committed = append(committed, typeName)
		}
	}
	sort.Strings(committed)

	if len(derived) != len(committed) {
		t.Fatalf("the derivation produces %d RecordBacked type(s) %v, the committed table carries %d %v",
			len(derived), derived, len(committed), committed)
	}
	for i := range derived {
		if derived[i] != committed[i] {
			t.Fatalf("derived RecordBacked set %v != committed %v", derived, committed)
		}
	}

	// Every derived row must also be exactly {Type, RecordBacked} - no
	// components, no reason, no identity attributes - or the derivation is
	// discarding a field a human ratified.
	for _, typeName := range derived {
		want := identity.TypeIdentity{Type: typeName, RecordBacked: true}
		if got := identity.DefaultTable[typeName]; got.Type != want.Type ||
			!got.RecordBacked || len(got.Components) != 0 || got.Reason != "" || got.IdentityAttrs != nil {
			t.Errorf("%s's committed row carries more than the derivation can produce: %+v", typeName, got)
		}
	}
}

// TestRecordBackedDerivationRefusesToDropARow is the adversarial half: mutate
// the evidence so the rule stops reaching rows the table already carries, and
// require recordBackedRows to refuse rather than emit a table with those rows
// deleted.
//
// This is the shape that matters most about this change. A RecordBacked row
// that vanishes from the table is a resource type that resolves today and
// stops resolving after a generator run, with no diagnostic anywhere saying
// so - the failure surfaces to an operator as "Resource type outside the
// live-markers subset" on a configuration that has not changed.
func TestRecordBackedDerivationRefusesToDropARow(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	// Clearing store_only on every provider empties the derived set without
	// touching a single type name, which is the realistic way this breaks:
	// a re-acquisition against a source list someone edited.
	mutated := logicalSchemas{}
	for _, p := range art.Providers {
		p.StoreOnly = false
		mutated.Providers = append(mutated.Providers, p)
	}

	backed, err := recordBackedRows(mutated)
	if err == nil {
		t.Fatalf("recordBackedRows accepted evidence deriving %d rows where the table carries more; "+
			"the drop guard is not firing", len(backed))
	}
	if backed != nil {
		t.Errorf("recordBackedRows returned a row set alongside its error; a refused derivation must hand back nothing")
	}
	for _, typeName := range []string{"null_resource", "terraform_data", "time_static"} {
		if !strings.Contains(err.Error(), typeName) {
			t.Errorf("the drop error does not name %s: %v", typeName, err)
		}
	}
}

// TestRecordBackedDerivationAdmitsAnUnseenType is the parity assertion this
// whole change exists for: a resource type nobody has written a row for -
// including one that does not exist yet - derives a row from the rule alone,
// with no edit anywhere in this generator.
//
// The synthetic type is deliberately not a plausible name. If the rule had a
// list of type names hiding in it anywhere, this fails.
func TestRecordBackedDerivationAdmitsAnUnseenType(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	var storeOnly, other int
	for i, p := range art.Providers {
		if p.StoreOnly {
			storeOnly = i
		} else {
			other = i
		}
	}

	future := logicalTypeSchema{Type: "random_zzz_not_yet_released"}
	art.Providers[storeOnly].Types = append(art.Providers[storeOnly].Types, future)

	// The same type under a provider whose resources are not store-only must
	// NOT derive a row, so the flag is doing real work rather than decorating
	// the artifact.
	art.Providers[other].Types = append(art.Providers[other].Types, logicalTypeSchema{Type: "local_zzz_not_yet_released"})

	derived := map[string]bool{}
	for _, typeName := range recordBackedTypes(art) {
		derived[typeName] = true
	}
	if !derived[future.Type] {
		t.Errorf("a new non-secret type of a store-only provider does not derive a RecordBacked row; "+
			"the rule is not general (derived: %d types)", len(derived))
	}
	if derived["local_zzz_not_yet_released"] {
		t.Errorf("a type of a provider marked store_only=false derived a RecordBacked row; the flag is not load-bearing")
	}
}

// TestSensitiveTypesDeriveNoRow pins the other direction of the rule against
// the committed evidence, recomputed rather than restated: every type of a
// store-only provider with a live sensitive attribute must be absent from the
// derived set, and the set of such types must be non-empty (a rule that
// refuses nothing is not being exercised).
func TestSensitiveTypesDeriveNoRow(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	derived := map[string]bool{}
	for _, typeName := range recordBackedTypes(art) {
		derived[typeName] = true
	}

	var refused []string
	for _, p := range art.Providers {
		if !p.StoreOnly {
			continue
		}
		for _, ty := range p.Types {
			if len(liveSensitiveAttrs(ty)) == 0 {
				continue
			}
			refused = append(refused, ty.Type)
			if derived[ty.Type] {
				t.Errorf("%s has live sensitive attribute(s) %v but derived a RecordBacked row", ty.Type, liveSensitiveAttrs(ty))
			}
		}
	}
	sort.Strings(refused)
	if len(refused) == 0 {
		t.Fatalf("no type of any store-only provider carries a live sensitive attribute; the secret half of the rule is not exercised by the committed evidence")
	}
	t.Logf("%d store-only type(s) refused for secret material: %v", len(refused), refused)
}

// TestDeprecationClauseBound bounds the subtraction in [liveSensitiveAttrs]
// over the committed artifact, so widening it later is a deliberate act with
// a visible diff. The audit shape is "a mask wider than its label": a
// subtraction that looks like it touches one type and in fact suppresses
// evidence on several.
//
// It recomputes both halves rather than restating them. internal/live/lint
// holds the same bound against its own frozen copy of this measurement; this
// one holds it against the artifact -emit actually reads.
func TestDeprecationClauseBound(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	var moved []string
	for _, p := range art.Providers {
		for _, ty := range p.Types {
			if len(ty.Sensitive) > 0 && len(liveSensitiveAttrs(ty)) == 0 {
				moved = append(moved, ty.Type)
			}
		}
	}
	sort.Strings(moved)

	if len(moved) != 1 || moved[0] != "local_file" {
		t.Errorf("the deprecation clause changes the sensitivity verdict of %v, want [local_file] - "+
			"re-read liveSensitiveAttrs' doc comment before widening this", moved)
	}
	// And it must change no derived row, because local_file's provider is not
	// store-only in the first place.
	for _, typeName := range moved {
		for _, p := range art.Providers {
			if !p.StoreOnly {
				continue
			}
			for _, ty := range p.Types {
				if ty.Type == typeName {
					t.Errorf("the deprecation clause moves %s, which belongs to a store-only provider, so it changes a derived row", typeName)
				}
			}
		}
	}
}

// TestLogicalSchemasPathIsUnderLive is a one-line guard on the artifact's
// home: it belongs beside every other pinned-evidence artifact, not inside
// the tool.
func TestLogicalSchemasPathIsUnderLive(t *testing.T) {
	if dir := filepath.Dir(logicalSchemasJSONRel); dir != "live" {
		t.Errorf("logicalSchemasJSONRel = %q, want a path under live/", logicalSchemasJSONRel)
	}
}
