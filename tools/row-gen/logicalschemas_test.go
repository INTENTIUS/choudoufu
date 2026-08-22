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

	// Dropping every measured type empties the derived set, which is the
	// realistic way this breaks: a re-acquisition against a provider that
	// failed to launch, or a source list someone edited.
	//
	// The mutation has moved twice, and both moves say something about the
	// rule rather than about the test. It cleared store_only first, which
	// stopped emptying anything once issue #314 made StoreOnly select a
	// class rather than gate a row. It then marked one attribute of every
	// type sensitive, which stopped emptying anything once issue #365 slice
	// 3 made sensitivity set [identity.TypeIdentity.SecretMaterial] on a row
	// rather than withhold the row - a secret-bearing type is record-backed
	// exactly as its siblings are, and what varies is whether the operator
	// asked for the record to be written. A mutation that no longer mutates
	// is a guard that passes forever, and the whole point of this test is
	// the opposite.
	mutated := logicalSchemas{}
	for _, p := range art.Providers {
		p.Types = nil
		mutated.Providers = append(mutated.Providers, p)
	}

	backed, _, err := recordBackedRows(loadRatifiedForTest(t), mutated)
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
// The synthetic types are deliberately not plausible names. If the rule had a
// list of type names hiding in it anywhere, this fails.
//
// It also pins where StoreOnly is and is NOT load-bearing, which issue #314
// moved. It used to require that a non-store-only provider's type derive NO
// RecordBacked row; that was wrong, and this test asserting it is part of why
// it survived three issues. hashicorp/local 2.9.0 implements no ImportState
// at all, so the record is the ONLY carrier that can bring a local_file's
// prior state back - it is record-backed for a reason StoreOnly does not
// speak to. What StoreOnly decides is the lint class, and
// [TestLogicalClassRowsSplitTheAdmittedClassesByStoreOnly] is where that is
// now pinned.
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

	// The same shape under a provider whose resources are not store-only
	// derives a row too - the record is what holds its prior state either
	// way - and so does a SENSITIVE type of either provider, which is the
	// half that moved with GitHub issue #365 slice 3: the record is where
	// such a type's prior state comes from whatever the operator's secrets
	// setting says, and the setting decides whether the record is written,
	// not whether it could be. What the sensitive attribute now sets is the
	// second flag, asserted below.
	art.Providers[other].Types = append(art.Providers[other].Types,
		logicalTypeSchema{Type: "local_zzz_not_yet_released"},
		logicalTypeSchema{Type: "local_zzz_secret", Sensitive: []logicalAttr{{Name: "content"}}})

	derived := map[string]bool{}
	for _, typeName := range recordBackedTypes(art) {
		derived[typeName] = true
	}
	secret := map[string]bool{}
	for _, typeName := range secretMaterialTypes(art) {
		secret[typeName] = true
	}
	if !derived[future.Type] {
		t.Errorf("a new non-secret type of a store-only provider does not derive a RecordBacked row; "+
			"the rule is not general (derived: %d types)", len(derived))
	}
	if !derived["local_zzz_not_yet_released"] {
		t.Error("a new non-secret type of a non-store-only provider derives no RecordBacked row; " +
			"its prior state has nowhere else to come from - hashicorp/local implements no import at all")
	}
	if !derived["local_zzz_secret"] {
		t.Error("a new type with a live sensitive attribute derived no RecordBacked row; " +
			"the record is where such a type's prior state comes from whatever the secrets setting says - " +
			"withholding the row would leave internal/live/identity with nothing to refuse WITH, and the " +
			"operator would get \"Resource type outside the live-markers subset\" instead of the setting's name")
	}
	if !secret["local_zzz_secret"] {
		t.Error("a new type with a live sensitive attribute derived no SecretMaterial flag; " +
			"the no-secrets toggle has nothing to gate on and the record would be written under either setting")
	}
	if secret["local_zzz_not_yet_released"] || secret[future.Type] {
		t.Error("a type with no live sensitive attribute derived a SecretMaterial flag; " +
			"strict { secrets = \"refuse\" } would refuse a type that generates no secret")
	}
}

// TestLogicalClassRowsSplitTheAdmittedClassesByStoreOnly is where StoreOnly's
// real job is pinned since issue #314: not whether a type derives a row, but
// which ADMITTED class the row carries.
//
// The distinction is the count.index walk. A RECORD_ADMITTED type's identity
// is the record addressed by its own instance address, so
// internal/live/lint's countIndexScopeForType skips the walk for it; an
// EXTERNAL_ADMITTED type has an argument naming an object the record does not
// bound, so the walk runs. Getting this backwards silences a real safety
// check, which is what internal/live/lint's TestLocalFileKeepsItsCountIndexCheck
// catches from the other side.
//
// Both halves are mutated, so neither passes by the artifact happening to
// have one provider of each kind.
func TestLogicalClassRowsSplitTheAdmittedClassesByStoreOnly(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	classOf := func(a logicalSchemas, typeName string) string {
		t.Helper()
		rows, err := logicalClassRows(a)
		if err != nil {
			t.Fatalf("logicalClassRows: %v", err)
		}
		for _, r := range rows {
			if r.Type == typeName {
				return r.Class
			}
		}
		return ""
	}

	for i, p := range art.Providers {
		if len(p.Types) == 0 {
			continue
		}
		typeName := p.Types[0].Type
		if len(liveSensitiveAttrs(p.Types[0])) > 0 {
			continue // SECRET_REFUSED either way; not what this checks
		}

		flipped := logicalSchemas{Providers: append([]logicalProviderSchemas(nil), art.Providers...)}
		flipped.Providers[i].StoreOnly = !p.StoreOnly

		asIs, want := classOf(art, typeName), logicalClassRecordAdmitted
		flip, wantFlip := classOf(flipped, typeName), logicalClassExternalAdmitted
		if !p.StoreOnly {
			want, wantFlip = wantFlip, want
		}
		if asIs != want {
			t.Errorf("%s (store_only=%v) derives %s, want %s", typeName, p.StoreOnly, asIs, want)
		}
		if flip != wantFlip {
			t.Errorf("%s with store_only flipped to %v derives %s, want %s - StoreOnly is not "+
				"selecting the admitted class", typeName, !p.StoreOnly, flip, wantFlip)
		}
	}
}

// TestSensitiveTypesDeriveASecretMaterialRow pins the other direction of the
// rule against the committed evidence, recomputed rather than restated: every
// measured type with a live sensitive attribute derives a RecordBacked row
// AND the SecretMaterial flag, and every type without one derives the row
// without the flag. The set of flagged types must be non-empty - a rule that
// flags nothing is not being exercised.
//
// It used to assert the opposite for the first half ("derived no row at
// all"), and GitHub issue #365 slice 3 is the reversal. Withholding the row
// answered the wrong question: whether the record CAN hold the type's prior
// state, which it always could, rather than whether the operator asked for it
// to. The visible cost of the old answer was that a configuration stock
// OpenTofu runs - one random_password - could not run here at all, and the
// message an operator got named the admission table rather than a setting.
func TestSensitiveTypesDeriveASecretMaterialRow(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	derived := map[string]bool{}
	for _, typeName := range recordBackedTypes(art) {
		derived[typeName] = true
	}
	flagged := map[string]bool{}
	for _, typeName := range secretMaterialTypes(art) {
		flagged[typeName] = true
	}

	var secret []string
	for _, p := range art.Providers {
		for _, ty := range p.Types {
			if !derived[ty.Type] {
				t.Errorf("%s is measured but derives no RecordBacked row at all", ty.Type)
			}
			if len(liveSensitiveAttrs(ty)) == 0 {
				if flagged[ty.Type] {
					t.Errorf("%s has no live sensitive attribute but derives SecretMaterial", ty.Type)
				}
				continue
			}
			secret = append(secret, ty.Type)
			if !flagged[ty.Type] {
				t.Errorf("%s has live sensitive attribute(s) %v and derives no SecretMaterial flag", ty.Type, liveSensitiveAttrs(ty))
			}
		}
	}
	sort.Strings(secret)
	if len(secret) == 0 {
		t.Fatalf("no measured type carries a live sensitive attribute; the secret half of the rule is not exercised by the committed evidence")
	}
	t.Logf("%d type(s) carry secret material: %v", len(secret), secret)
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
	// Both ADMITTED classes, not just RECORD_ADMITTED. Since issue #314 the
	// lint side of this equality is a set of two classes - a record_store
	// admits either - and comparing only one half against the whole
	// RecordBacked set would report the other half as a divergence, which is
	// exactly the false alarm that makes a guard get relaxed.
	// A row lint admits under SOME setting: its own class is an admitted
	// one, or its StoredClass is (GitHub issue #365 slice 3, where a
	// SECRET_REFUSED row started carrying the class it would have had if
	// nothing in its schema were sensitive). That is the set identity's
	// RecordBacked half has to equal - the identity row exists so that the
	// two layers have something to agree ABOUT, and lint's secrets setting
	// then decides which of them a given run admits.
	admitted := map[string]bool{}
	classes := map[string]string{}
	secretRefused := map[string]bool{}
	for _, r := range rows {
		classes[r.Type] = r.Class
		switch {
		case r.Class == logicalClassRecordAdmitted || r.Class == logicalClassExternalAdmitted:
			admitted[r.Type] = true
		case r.StoredClass == logicalClassRecordAdmitted || r.StoredClass == logicalClassExternalAdmitted:
			admitted[r.Type] = true
			secretRefused[r.Type] = true
		}
	}
	backed := map[string]bool{}
	for _, typeName := range recordBackedTypes(art) {
		backed[typeName] = true
	}
	flagged := map[string]bool{}
	for _, typeName := range secretMaterialTypes(art) {
		flagged[typeName] = true
	}

	if len(admitted) == 0 {
		t.Fatal("no admitted row derived at all; the rule is not being exercised")
	}
	if len(secretRefused) == 0 {
		t.Fatal("no SECRET_REFUSED row carries a StoredClass; the secrets setting has nothing to admit and this " +
			"check has degenerated into the pre-#365 equality")
	}
	for typeName := range admitted {
		if !backed[typeName] {
			t.Errorf("%s derives %s for lint but no RecordBacked row for identity: "+
				"lint would admit under a record_store what resolution then refuses",
				typeName, classes[typeName])
		}
	}
	for typeName := range backed {
		if !admitted[typeName] {
			t.Errorf("%s derives a RecordBacked identity row but lint's class is %q with no StoredClass: "+
				"resolution would hold a record for a type lint refuses under every setting", typeName, classes[typeName])
		}
	}
	// The two flags are one predicate read twice and must name one set: a
	// row lint calls SECRET_REFUSED is exactly a row identity flags
	// SecretMaterial. Divergence here is the shape that would let lint
	// admit under secrets=store a type whose identity row does not know it
	// holds a secret, so the resolver's own gate would never fire.
	for typeName := range secretRefused {
		if !flagged[typeName] {
			t.Errorf("%s is SECRET_REFUSED for lint but derives no SecretMaterial flag for identity: "+
				"the resolver's own secrets gate would never fire for it", typeName)
		}
	}
	for typeName := range flagged {
		if !secretRefused[typeName] {
			t.Errorf("%s derives SecretMaterial for identity but lint's class is %q: "+
				"the resolver would refuse under secrets=refuse what lint had already admitted", typeName, classes[typeName])
		}
	}

	// Each class must actually be populated, or the loop above degenerates
	// into checking one class against itself and the equality it guards stops
	// being an equality between two different things.
	var recordAdmitted, externalAdmitted int
	for typeName := range admitted {
		if classes[typeName] == logicalClassExternalAdmitted {
			externalAdmitted++
		} else {
			recordAdmitted++
		}
	}
	if recordAdmitted == 0 || externalAdmitted == 0 {
		t.Errorf("admitted rows split %d RECORD_ADMITTED / %d EXTERNAL_ADMITTED; both must be "+
			"non-zero or this test only exercises one side of the class boundary",
			recordAdmitted, externalAdmitted)
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
	if got, want := string(lint.ClassExternalAdmitted), "EXTERNAL_ADMITTED"; got != want {
		t.Errorf("lint.ClassExternalAdmitted = %q, want %q", got, want)
	}
	if got, want := string(lint.ClassSecretRefused), "SECRET_REFUSED"; got != want {
		t.Errorf("lint.ClassSecretRefused = %q, want %q", got, want)
	}
	for _, name := range []string{logicalClassRecordAdmitted, logicalClassExternalAdmitted, logicalClassSecretRefused} {
		if !strings.HasPrefix(name, "Class") {
			t.Errorf("rendered class identifier %q is not one of lint's exported Class* constants", name)
		}
	}
}

// TestLogicalClassRowsCoverEveryMeasuredType pins that the derivation is
// total over the artifact, recomputed from it: every measured type gets a row
// and a verdict, whoever serves it.
//
// It used to require the opposite of hashicorp/local's two types - a row per
// store-only type and NONE from the rest - on the reasoning that a row would
// have promoted local_file to RECORD_ADMITTED and silenced its count.index
// walk. Issue #314 separated those two things: local_file now has a row, in a
// class that keeps the walk, and internal/live/lint's
// TestLocalFileKeepsItsCountIndexCheck still passes unchanged.
//
// What the coverage bound buys is that OTHER_REFUSED - the class with no
// evidence behind it - is reachable only by a type this generator has never
// measured. Every measured type is entitled to a measured verdict.
func TestLogicalClassRowsCoverEveryMeasuredType(t *testing.T) {
	art := loadLogicalSchemasForTest(t)

	rows, err := logicalClassRows(art)
	if err != nil {
		t.Fatalf("logicalClassRows: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.Type] = r.Class
		if r.Evidence == "" {
			t.Errorf("%s derives an empty Evidence string", r.Type)
		}
		// External is the EXTERNAL_ADMITTED class's own evidence and says
		// nothing true about any other, which internal/live/lint's
		// TestLogicalTypesTableWellFormed pins on the emitted table. Pinned
		// here too, at the source, so the generator cannot start populating
		// it everywhere and have the emitted table be the only thing that
		// notices.
		// Since GitHub issue #365 slice 3 the class that owns External is
		// the one the row would carry under `secrets = "store"`, which for
		// a SECRET_REFUSED row is its StoredClass. local_sensitive_file is
		// the case: a hashicorp/local type, so the record does not bound
		// what it affects, and its own schema is sensitive - both facts are
		// true at once and the row has to carry both.
		effective := r.Class
		if r.StoredClass != "" {
			effective = r.StoredClass
		}
		if (r.External != "") != (effective == logicalClassExternalAdmitted) {
			t.Errorf("%s is %s (stored: %s) with External=%q; that field belongs to EXTERNAL_ADMITTED and to no "+
				"other class", r.Type, r.Class, r.StoredClass, r.External)
		}
	}

	wantCount, nonStoreOnly := 0, 0
	for _, p := range art.Providers {
		for _, ty := range p.Types {
			wantCount++
			if !p.StoreOnly {
				nonStoreOnly++
			}
			if _, ok := got[ty.Type]; !ok {
				t.Errorf("%s is served by %s but derives no logicalTypes row", ty.Type, p.Source)
			}
		}
	}
	if len(rows) != wantCount {
		t.Errorf("derived %d rows, want %d (every measured type)", len(rows), wantCount)
	}
	if nonStoreOnly == 0 {
		t.Error("no non-store-only type in the artifact; the StoreOnly class split is not being exercised")
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
