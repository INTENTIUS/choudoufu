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
	"github.com/intentius/choudoufu/internal/live/lint"
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

// TestLogicalClassRowsAgreeWithRecordBackedDerivation is the layer-agreement
// check at the generator, one level up from internal/live/lint's own.
//
// [logicalClassRows] and [recordBackedTypes] are two readings of one rule over
// one artifact - lint's RECORD_ADMITTED and identity's RecordBacked - and the
// whole reason both are derived here is that they were separately maintained
// and drifted. This recomputes both from the committed artifact and asserts
// the two sets are equal, so a change to either rule that does not move the
// other stops the build rather than shipping a lint that refuses what
// resolution admits.
func TestLogicalClassRowsAgreeWithRecordBackedDerivation(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	rows, err := logicalClassRows(art)
	if err != nil {
		t.Fatalf("logicalClassRows: %v", err)
	}
	admitted := map[string]bool{}
	for _, r := range rows {
		if r.Class == logicalClassRecordAdmitted {
			admitted[r.Type] = true
		}
	}
	backed := map[string]bool{}
	for _, typeName := range recordBackedTypes(art) {
		backed[typeName] = true
	}

	if len(admitted) == 0 {
		t.Fatal("no RECORD_ADMITTED row derived at all; the rule is not being exercised")
	}
	for typeName := range admitted {
		if !backed[typeName] {
			t.Errorf("%s derives RECORD_ADMITTED for lint but no RecordBacked row for identity: "+
				"lint would admit under a record_store what resolution then refuses", typeName)
		}
	}
	for typeName := range backed {
		if !admitted[typeName] {
			t.Errorf("%s derives a RecordBacked identity row but not lint's RECORD_ADMITTED: "+
				"resolution would hold a record for a type lint refuses first", typeName)
		}
	}
}

// TestGeneratedLogicalClassNamesMatchLint checks the two class names this
// generator renders as source text against internal/live/lint's own
// constants. The rendered file compiles against them, so a rename breaks the
// build - but it breaks it in generated output, at whatever moment someone
// next runs -emit, with no clue where the string came from. This names the
// pairing directly.
func TestGeneratedLogicalClassNamesMatchLint(t *testing.T) {
	if got, want := string(lint.ClassRecordAdmitted), "RECORD_ADMITTED"; got != want {
		t.Errorf("lint.ClassRecordAdmitted = %q, want %q", got, want)
	}
	if got, want := string(lint.ClassSecretRefused), "SECRET_REFUSED"; got != want {
		t.Errorf("lint.ClassSecretRefused = %q, want %q", got, want)
	}
	for _, name := range []string{logicalClassRecordAdmitted, logicalClassSecretRefused} {
		if !strings.HasPrefix(name, "Class") {
			t.Errorf("rendered class identifier %q is not one of lint's exported Class* constants", name)
		}
	}
}

// TestLogicalClassRowsCoverEveryStoreOnlyType pins that the derivation is
// total over the store-only providers and empty over the rest, recomputed
// from the artifact. A row per store-only type and none from hashicorp/local
// is what keeps local_file on lint's OTHER_REFUSED default, which
// internal/live/lint's TestLocalFileKeepsItsCountIndexCheck depends on.
func TestLogicalClassRowsCoverEveryStoreOnlyType(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	rows, err := logicalClassRows(art)
	if err != nil {
		t.Fatalf("logicalClassRows: %v", err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.Type] = true
		if r.Evidence == "" {
			t.Errorf("%s derives an empty Evidence string", r.Type)
		}
	}

	wantCount, excluded := 0, 0
	for _, p := range art.Providers {
		for _, ty := range p.Types {
			if p.StoreOnly {
				wantCount++
				if !got[ty.Type] {
					t.Errorf("%s is served by store-only %s but derives no logicalTypes row", ty.Type, p.Source)
				}
				continue
			}
			excluded++
			if got[ty.Type] {
				t.Errorf("%s is served by %s, which is not store-only, but derives a logicalTypes row anyway",
					ty.Type, p.Source)
			}
		}
	}
	if len(rows) != wantCount {
		t.Errorf("derived %d rows, want %d (every store-only type, and only those)", len(rows), wantCount)
	}
	if excluded == 0 {
		t.Error("no non-store-only type in the artifact; the StoreOnly exclusion is not being exercised")
	}
}

// TestLogicalFamilyPrefixDropsTheBuiltin pins the one asymmetry in the prefix
// derivation. Every registry provider contributes its family prefix; the
// built-in contributes none, because "terraform_" would claim a whole family
// on the strength of one built-in type, and terraform_data is admitted by
// exact name instead.
func TestLogicalFamilyPrefixDropsTheBuiltin(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	prefixes := logicalFamilyPrefixesOf(art)
	for _, p := range prefixes {
		if p == "terraform_" {
			t.Error("logicalFamilyPrefixesOf yielded \"terraform_\"; the built-in provider must contribute no family prefix")
		}
		if !strings.HasSuffix(p, "_") {
			t.Errorf("family prefix %q does not end in an underscore", p)
		}
	}
	if got, want := len(prefixes), len(art.Providers)-1; got != want {
		t.Errorf("derived %d family prefixes from %d providers, want %d (all but the built-in)",
			got, len(art.Providers), want)
	}

	// terraform_data must still get a row, with no prefix - the gap the
	// original hand table existed to close.
	rows, err := logicalClassRows(art)
	if err != nil {
		t.Fatalf("logicalClassRows: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.Type != "terraform_data" {
			continue
		}
		found = true
		if r.Prefix != "" {
			t.Errorf("terraform_data derives Prefix %q, want empty", r.Prefix)
		}
	}
	if !found {
		t.Error("terraform_data derives no logicalTypes row")
	}
}

// TestLogicalClassRowsRefuseAMismatchedPrefix defeats the prefix derivation
// rather than confirming it: a store-only provider serving a type that does
// not start with its own source-derived prefix must stop the run. lint
// classifies unlisted family members by prefix, so a provider whose types do
// not share one would have its unlisted types classified by nobody.
func TestLogicalClassRowsRefuseAMismatchedPrefix(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	for i, p := range art.Providers {
		if !p.StoreOnly || p.Source == builtinProviderSource {
			continue
		}
		art.Providers[i].Types = append(art.Providers[i].Types, logicalTypeSchema{Type: "wrongprefix_thing"})
		break
	}
	if _, err := logicalClassRows(art); err == nil {
		t.Error("a store-only provider serving a type outside its own prefix derived rows without complaint")
	}
}

// TestLogicalEvidenceRendersTheDeprecatedOnlyShape exercises the one branch
// of [logicalEvidence] the committed artifact cannot reach: a store-only
// type whose only sensitive attributes are deprecated. It derives
// RECORD_ADMITTED, and the evidence must say so honestly rather than claiming
// the schema marks nothing sensitive - which is what the plain branch would
// have said about a schema that plainly does.
//
// Nothing in the artifact hits it today: the one attribute in the whole
// measurement that is both sensitive and deprecated is
// local_file.sensitive_content, and hashicorp/local is not store-only. A
// provider release could change that at any time.
func TestLogicalEvidenceRendersTheDeprecatedOnlyShape(t *testing.T) {
	p := logicalProviderSchemas{Source: "hashicorp/random", Version: "9.9.9", StoreOnly: true}
	ty := logicalTypeSchema{
		Type:       "random_thing",
		Sensitive:  []logicalAttr{{Name: "old_secret"}},
		Deprecated: []logicalAttr{{Name: "old_secret", DeprecationMessage: "Use random_password instead"}},
	}
	if got := len(liveSensitiveAttrs(ty)); got != 0 {
		t.Fatalf("liveSensitiveAttrs = %d, want 0; the fixture does not reach the branch", got)
	}

	got := logicalEvidence(p, ty)
	for _, want := range []string{"old_secret", "deprecates", "Use random_password instead"} {
		if !strings.Contains(got, want) {
			t.Errorf("evidence %q does not mention %q", got, want)
		}
	}
	if strings.Contains(got, "marks no attribute") {
		t.Errorf("evidence %q claims the schema marks no attribute sensitive, but it marks old_secret", got)
	}
}

// TestJoinAnd covers the prose joiner the evidence strings are built with.
func TestJoinAnd(t *testing.T) {
	for _, test := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a and b"},
		{[]string{"a", "b", "c"}, "a, b and c"},
	} {
		if got := joinAnd(test.in); got != test.want {
			t.Errorf("joinAnd(%v) = %q, want %q", test.in, got, test.want)
		}
	}
}
