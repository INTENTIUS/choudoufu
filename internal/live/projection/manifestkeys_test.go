// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1211's unit half. The two arms that matter are the same
// two the smoke claim runs on a cluster:
//
//  1. a label the estate's record says it declared, and the configuration
//     no longer does, enters the prior - which is what makes the removal
//     plan;
//  2. a label nothing in this estate ever declared never does, however
//     live it is and whoever metadata.managedFields says wrote it,
//     because mirroring it proposes a deletion the server undoes and the
//     plan then churns for ever.
//
// Arm 2 is the one the naive widening fails and the one that refuted the
// managedFields route, so every case below that asserts a key is ABSENT is
// load-bearing: delete those assertions and both rejected designs pass
// this file.

// removedSet is one removal set, spelled the way the mirror reads it: per
// metadata map, the keys the record named and the configuration no longer
// does.
func removedSet(labels, annotations []string) map[string]map[string]bool {
	out := map[string]map[string]bool{
		markers.LabelSurfaceAttr:      {},
		markers.AnnotationSurfaceAttr: {},
	}
	for _, k := range labels {
		out[markers.LabelSurfaceAttr][k] = true
	}
	for _, k := range annotations {
		out[markers.AnnotationSurfaceAttr][k] = true
	}
	return out
}

// TestMirrorManifestComputedFieldsPlansARecordedRemoval is arm 1. The
// configuration used to declare squad and owner and no longer does; the
// live object still has them. They must be in the prior, holding the LIVE
// value, so the provider sees the configuration differ from the prior and
// plans the removal.
func TestMirrorManifestComputedFieldsPlansARecordedRemoval(t *testing.T) {
	block := manifestTypeSchema().Block
	in := manifestReadValue(
		// What the configuration says NOW: squad and owner are gone.
		map[string]cty.Value{
			"labels":      priorObjectMap(map[string]string{"tier": "one", markers.TagEstate: "smoke-crd"}),
			"annotations": priorObjectMap(map[string]string{"reviewed": "yes"}),
		},
		// What the object still holds.
		map[string]cty.Value{
			"labels":      liveStringMap(map[string]string{"tier": "one", markers.TagEstate: "smoke-crd", "squad": "blue"}),
			"annotations": liveStringMap(map[string]string{"reviewed": "yes", "owner": "payments"}),
		},
	)
	removed := removedSet([]string{"squad"}, []string{"owner"})

	// The negative control, and the whole reason this fix exists: with no
	// removal set, the removed keys are in neither the prior nor the
	// configuration, the two agree, and nothing plans.
	before := priorMapOf(t, mirrorManifestComputedFields(in, block, nil), "labels")
	if _, has := before["squad"]; has {
		t.Fatal("the pre-#1211 mirror already carried the removed key; this test proves nothing")
	}

	got := mirrorManifestComputedFields(in, block, removed)
	labels := priorMapOf(t, got, "labels")
	if labels["squad"] != "blue" {
		t.Errorf("labels[squad] = %q, want the live value %q so the removal can plan", labels["squad"], "blue")
	}
	if labels["tier"] != "one" || labels[markers.TagEstate] != "smoke-crd" {
		t.Errorf("a still-declared label was disturbed: %v", labels)
	}
	ann := priorMapOf(t, got, "annotations")
	if ann["owner"] != "payments" {
		t.Errorf("annotations[owner] = %q, want the live value %q", ann["owner"], "payments")
	}
	if ann["reviewed"] != "yes" {
		t.Errorf("a still-declared annotation was disturbed: %v", ann)
	}
}

// TestMirrorManifestComputedFieldsLeavesForeignKeysAlone is arm 2, and it
// is the test both rejected designs fail. Three keys the live object
// carries that this estate never declared: one from `kubectl label`, the
// API server's own on a Namespace, and a controller's annotation. None may
// enter the prior, because the server writes each of them straight back
// and a plan that proposes deleting them never converges.
func TestMirrorManifestComputedFieldsLeavesForeignKeysAlone(t *testing.T) {
	block := manifestTypeSchema().Block
	in := manifestReadValue(
		map[string]cty.Value{
			"labels":      priorObjectMap(map[string]string{markers.TagEstate: "smoke-crd"}),
			"annotations": priorObjectMap(map[string]string{"reviewed": "yes"}),
		},
		map[string]cty.Value{
			"labels": liveStringMap(map[string]string{
				markers.TagEstate:             "smoke-crd",
				"external":                    "keepme",
				"kubernetes.io/metadata.name": "lbl1211",
			}),
			"annotations": liveStringMap(map[string]string{
				"reviewed": "yes",
				"kubectl.kubernetes.io/last-applied-configuration": "{}",
			}),
		},
	)
	// The record names only what the configuration declares today, so
	// nothing at all was removed from it.
	got := mirrorManifestComputedFields(in, block, removedSet(nil, nil))
	labels := priorMapOf(t, got, "labels")
	if len(labels) != 1 || labels[markers.TagEstate] != "smoke-crd" {
		t.Errorf("labels = %v, want only the marker; anything else plans a deletion the server undoes", labels)
	}
	ann := priorMapOf(t, got, "annotations")
	if len(ann) != 1 || ann["reviewed"] != "yes" {
		t.Errorf("annotations = %v, want only the declared one", ann)
	}
}

// TestMirrorManifestComputedFieldsIgnoresARemovedKeyTheObjectHasNot: the
// record names a key some later writer has already removed. There is
// nothing to mirror and nothing to remove, and inventing a prior entry for
// it would propose deleting a key that is not there. This is one of the
// two reasons a stale record cannot churn.
func TestMirrorManifestComputedFieldsIgnoresARemovedKeyTheObjectHasNot(t *testing.T) {
	block := manifestTypeSchema().Block
	in := manifestReadValue(
		map[string]cty.Value{"labels": priorObjectMap(map[string]string{markers.TagEstate: "smoke-crd"})},
		map[string]cty.Value{"labels": liveStringMap(map[string]string{markers.TagEstate: "smoke-crd"})},
	)
	got := mirrorManifestComputedFields(in, block, removedSet([]string{"gone"}, nil))
	if labels := priorMapOf(t, got, "labels"); len(labels) != 1 || labels[markers.TagEstate] != "smoke-crd" {
		t.Errorf("labels = %v, want only the marker", labels)
	}
	// And with nothing to change, the value comes back as it was.
	if !got.RawEquals(in) {
		t.Error("a no-op mirror rebuilt the value")
	}
}

// TestMirrorManifestComputedFieldsPlansTheLastKeyOfAMap: deleting the last
// annotation from a configuration deletes the `annotations` map with it, so
// the prior manifest has no such attribute to widen. The removed keys the
// object still carries go into a map of their own, or the removal is
// invisible - which is how PR #1259's first smoke step failed, on the
// commonest shape of all: one annotation, then none.
func TestMirrorManifestComputedFieldsPlansTheLastKeyOfAMap(t *testing.T) {
	block := manifestTypeSchema().Block
	in := manifestReadValue(
		// No "annotations" attribute at all: the configuration has none.
		map[string]cty.Value{"labels": priorObjectMap(map[string]string{markers.TagEstate: "smoke-crd"})},
		map[string]cty.Value{
			"labels": liveStringMap(map[string]string{markers.TagEstate: "smoke-crd"}),
			"annotations": liveStringMap(map[string]string{
				"reviewed": "yes",
				"kubectl.kubernetes.io/last-applied-configuration": "{}",
			}),
		},
	)
	if _, has := in.GetAttr("manifest").GetAttr("metadata").Type().AttributeTypes()["annotations"]; has {
		t.Fatal("the fixture declares annotations, so this test measures the wrong thing")
	}

	// Nothing removed there: the attribute stays absent, because absent on
	// both sides is agreement and there is nothing to remove.
	if got := mirrorManifestComputedFields(in, block, removedSet(nil, nil)); !got.RawEquals(in) {
		t.Error("a map the configuration never had and nothing was removed from was invented")
	}

	got := mirrorManifestComputedFields(in, block, removedSet(nil, []string{"reviewed"}))
	ann := priorMapOf(t, got, "annotations")
	if len(ann) != 1 || ann["reviewed"] != "yes" {
		t.Fatalf("annotations = %v, want only the removed key at its live value", ann)
	}
	if _, has := ann["kubectl.kubernetes.io/last-applied-configuration"]; has {
		t.Error("a foreign manager's annotation entered a prior map built from nothing")
	}
}

// removalLive is the world the candidate tests run against: the
// configuration declares tier and the marker plus one annotation, and the
// live object carries those plus squad, owner, a kubectl label and the API
// server's own key.
func removalLive() cty.Value {
	return manifestReadValue(
		map[string]cty.Value{
			"labels":      priorObjectMap(map[string]string{"tier": "one", markers.TagEstate: "smoke-crd"}),
			"annotations": priorObjectMap(map[string]string{"reviewed": "yes"}),
		},
		map[string]cty.Value{
			"labels": liveStringMap(map[string]string{
				"tier": "one", markers.TagEstate: "smoke-crd", "squad": "blue",
				"external": "keepme", "kubernetes.io/metadata.name": "lbl1211",
			}),
			"annotations": liveStringMap(map[string]string{"reviewed": "yes", "owner": "payments"}),
		},
	)
}

// recordedEverything is what the record holds after an apply of a
// configuration that DID declare squad and owner.
func recordedEverything() map[string][]string {
	return map[string][]string{
		markers.LabelSurfaceAttr:      {markers.TagEstate, "squad", "tier"},
		markers.AnnotationSurfaceAttr: {"owner", "reviewed"},
	}
}

// TestManifestRemovalCandidatesIsTheRecordMinusTheConfiguration pins the
// whole rule, including the two filters that are easy to lose: a key the
// configuration still declares is not a candidate, and neither is one the
// live object no longer carries.
func TestManifestRemovalCandidatesIsTheRecordMinusTheConfiguration(t *testing.T) {
	got, ok := manifestRemovalCandidates(removalLive(), recordedEverything())
	if !ok {
		t.Fatal("the manifest shape was not recognised")
	}
	if len(got[markers.LabelSurfaceAttr]) != 1 || !got[markers.LabelSurfaceAttr]["squad"] {
		t.Errorf("label candidates = %v, want only squad", got[markers.LabelSurfaceAttr])
	}
	if len(got[markers.AnnotationSurfaceAttr]) != 1 || !got[markers.AnnotationSurfaceAttr]["owner"] {
		t.Errorf("annotation candidates = %v, want only owner", got[markers.AnnotationSurfaceAttr])
	}

	// The keys nobody declared are not candidates, and this is arm 2 at
	// the source rather than at the mirror: a record cannot name them,
	// because the only way in is being declared at an apply.
	for _, key := range []string{"external", "kubernetes.io/metadata.name"} {
		if got[markers.LabelSurfaceAttr][key] {
			t.Errorf("%q became a removal candidate; the plan would churn against a server that writes it back", key)
		}
	}

	// A record that names a key the object has already lost proposes
	// nothing: one of the two reasons a stale record is harmless.
	stale := recordedEverything()
	stale[markers.LabelSurfaceAttr] = append(stale[markers.LabelSurfaceAttr], "vanished")
	got, _ = manifestRemovalCandidates(removalLive(), stale)
	if got[markers.LabelSurfaceAttr]["vanished"] {
		t.Error("a recorded key the live object no longer carries became a candidate")
	}
}

// TestManifestRemovalKeysCostsNothingWhenNothingWasRemoved is the
// short-circuit, and it is a behaviour rather than an optimisation: on a
// converged estate the safety rail must not be consulted at all, so no
// cluster round trip happens per instance per plan and no warning can
// fire over a removal nobody asked for.
func TestManifestRemovalKeysCostsNothingWhenNothingWasRemoved(t *testing.T) {
	called := false
	hook := func(context.Context, ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
		called = true
		return ManifestOwnedKeys{}, errors.New("the rail must not have been consulted")
	}

	// Converged: the record says exactly what the configuration declares.
	converged := map[string][]string{
		markers.LabelSurfaceAttr:      {markers.TagEstate, "tier"},
		markers.AnnotationSurfaceAttr: {"reviewed"},
	}
	keys, diags := manifestRemovalKeys(context.Background(), removalLive(), &manifestKeyLookup{
		addr: manifestAddr(t), hook: hook, declared: converged,
	})
	if called {
		t.Error("the cluster was asked about managedFields with no removal pending")
	}
	if keys != nil || len(diags) != 0 {
		t.Errorf("keys = %v, diags = %v, want neither", keys, diags)
	}

	// No record at all: the missing-record degradation. Quiet, and no
	// round trip either.
	keys, diags = manifestRemovalKeys(context.Background(), removalLive(), &manifestKeyLookup{
		addr: manifestAddr(t), hook: hook,
	})
	if called || keys != nil || len(diags) != 0 {
		t.Errorf("an instance with no record was not quiet: called = %v, keys = %v, diags = %v", called, keys, diags)
	}

	// Not a manifest-shaped type at all: every AWS read in the corpus.
	keys, diags = manifestRemovalKeys(context.Background(), removalLive(), nil)
	if keys != nil || len(diags) != 0 {
		t.Errorf("keys = %v, diags = %v, want neither", keys, diags)
	}
}

// TestManifestRemovalKeysIntersectsTheSafetyRail: the rail narrows and
// never widens. squad is ours and survives; owner has been taken over by
// another field manager and is dropped, with a warning that says so.
func TestManifestRemovalKeysIntersectsTheSafetyRail(t *testing.T) {
	keys, diags := manifestRemovalKeys(context.Background(), removalLive(), &manifestKeyLookup{
		addr:     manifestAddr(t),
		declared: recordedEverything(),
		hook: func(context.Context, ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
			return ManifestOwnedKeys{
				ManagedFieldsPresent: true,
				// Everything on the object except owner, which
				// `kubectl annotate` took over - plus the two foreign
				// keys the laundering hands us, which the rail must not
				// be able to turn into candidates.
				Keys: removedSet(
					[]string{markers.TagEstate, "tier", "squad", "external", "kubernetes.io/metadata.name"},
					[]string{"reviewed"},
				),
			}, nil
		},
	})
	if len(keys[markers.LabelSurfaceAttr]) != 1 || !keys[markers.LabelSurfaceAttr]["squad"] {
		t.Errorf("label removals = %v, want only squad", keys[markers.LabelSurfaceAttr])
	}
	if len(keys[markers.AnnotationSurfaceAttr]) != 0 {
		t.Errorf("annotation removals = %v, want none - owner is another manager's now", keys[markers.AnnotationSurfaceAttr])
	}
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %v, want exactly one warning about the declined key", diags)
	}
	if diags.HasErrors() {
		t.Error("a key another manager owns failed the estate")
	}
	if got := diags[0].Description().Summary; got != SummaryManifestRemovalUndetectable {
		t.Errorf("summary = %q, want %q", got, SummaryManifestRemovalUndetectable)
	}
	if detail := diags[0].Description().Detail; !strings.Contains(detail, "metadata.annotations.owner") {
		t.Errorf("detail = %q, want it to name the key it declined", detail)
	}
}

// TestManifestRemovalKeysDegradation pins what happens when the rail
// cannot be consulted and a removal IS pending. Every one of these ends in
// today's narrow prior, which plans No changes over a removed key - so
// every one of them must SAY so. A silent nil here is GitHub issue #1211
// restored.
func TestManifestRemovalKeysDegradation(t *testing.T) {
	addr := manifestAddr(t)

	warns := func(t *testing.T, lookup *manifestKeyLookup, want string) {
		t.Helper()
		lookup.declared = recordedEverything()
		keys, diags := manifestRemovalKeys(context.Background(), removalLive(), lookup)
		if keys != nil {
			t.Errorf("keys = %v, want none", keys)
		}
		if len(diags) != 1 {
			t.Fatalf("diagnostics = %d, want exactly one warning", len(diags))
		}
		if got := diags[0].Description().Summary; got != SummaryManifestRemovalUndetectable {
			t.Errorf("summary = %q, want %q", got, SummaryManifestRemovalUndetectable)
		}
		if diags.HasErrors() {
			t.Error("a cluster that would not answer a supplementary read failed the whole estate")
		}
		detail := diags[0].Description().Detail
		if !strings.Contains(detail, "metadata.labels.squad") {
			t.Errorf("detail = %q, want it to name the key that will not be removed", detail)
		}
		if want != "" && !strings.Contains(detail, want) {
			t.Errorf("detail = %q, want it to mention %q", detail, want)
		}
	}

	t.Run("no hook", func(t *testing.T) {
		warns(t, &manifestKeyLookup{addr: addr}, "no cluster client")
	})

	t.Run("cluster could not answer", func(t *testing.T) {
		warns(t, &manifestKeyLookup{addr: addr, hook: func(context.Context, ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
			return ManifestOwnedKeys{}, errors.New("connection refused")
		}}, "connection refused")
	})

	t.Run("object carries no managedFields", func(t *testing.T) {
		warns(t, &manifestKeyLookup{addr: addr, hook: func(context.Context, ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
			return ManifestOwnedKeys{Keys: removedSet(nil, nil)}, nil
		}}, "no metadata.managedFields")
	})

	t.Run("answer names no maps", func(t *testing.T) {
		warns(t, &manifestKeyLookup{addr: addr, hook: func(context.Context, ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
			return ManifestOwnedKeys{ManagedFieldsPresent: true}, nil
		}}, "no metadata.managedFields")
	})
}

// TestManifestRemovalKeysNamesTheLiveObject: the request carries the object
// the PROVIDER read, off its own `object` attribute, because that is the
// object whose managedFields answer the question.
func TestManifestRemovalKeysNamesTheLiveObject(t *testing.T) {
	addr := manifestAddr(t)
	var saw ManifestOwnedKeysRequest
	_, diags := manifestRemovalKeys(context.Background(), removalLive(), &manifestKeyLookup{
		addr: addr, declared: recordedEverything(),
		hook: func(_ context.Context, req ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
			saw = req
			return ManifestOwnedKeys{Keys: removedSet([]string{"squad"}, []string{"owner"}), ManagedFieldsPresent: true}, nil
		}})
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if saw.APIVersion != "stable.example.com/v1" || saw.Kind != "CronTab" {
		t.Errorf("apiVersion/kind = %q/%q, want the live object's own", saw.APIVersion, saw.Kind)
	}
	if saw.Namespace != "smoke-crd" || saw.Name != "my-crontab" {
		t.Errorf("namespace/name = %q/%q, want the live object's own", saw.Namespace, saw.Name)
	}
	if saw.Addr.String() != addr.String() {
		t.Errorf("addr = %s, want %s", saw.Addr, addr)
	}
}

// TestManifestRemovalKeysRefusesAnUnnameableObject: no apiVersion, kind and
// name means no object to ask about. It warns rather than asking about a
// half-named one.
func TestManifestRemovalKeysRefusesAnUnnameableObject(t *testing.T) {
	// A prior manifest declaring nothing, a live object declaring one
	// label and no name at all - so there is a candidate to act on and no
	// object to confirm it against.
	bare := cty.ObjectVal(map[string]cty.Value{
		markers.ManifestSurfaceAttr: cty.ObjectVal(map[string]cty.Value{
			"metadata": cty.EmptyObjectVal,
		}),
		markers.ManifestLiveAttr: cty.ObjectVal(map[string]cty.Value{
			"metadata": cty.ObjectVal(map[string]cty.Value{
				"labels": liveStringMap(map[string]string{"squad": "blue"}),
			}),
		}),
	})
	called := false
	keys, diags := manifestRemovalKeys(context.Background(), bare, &manifestKeyLookup{
		addr:     manifestAddr(t),
		declared: map[string][]string{markers.LabelSurfaceAttr: {"squad"}},
		hook: func(context.Context, ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
			called = true
			return ManifestOwnedKeys{}, nil
		}})
	if called {
		t.Error("the cluster was asked about an object with no name")
	}
	if keys != nil || len(diags) != 1 || diags[0].Description().Summary != SummaryManifestRemovalUndetectable {
		t.Errorf("keys = %v, diags = %v, want one warning and no keys", keys, diags)
	}
}

// TestManifestDeclaredKeysReadsTheAppliedManifest is the write half: what
// write-back records after an apply is the key set of the manifest that
// was SENT, marker included, and nothing off the live object.
func TestManifestDeclaredKeysReadsTheAppliedManifest(t *testing.T) {
	applied := manifestReadValue(
		map[string]cty.Value{
			"labels":      priorObjectMap(map[string]string{"tier": "one", markers.TagEstate: "smoke-crd"}),
			"annotations": priorObjectMap(map[string]string{"reviewed": "yes"}),
		},
		map[string]cty.Value{
			"labels":      liveStringMap(map[string]string{"tier": "one", markers.TagEstate: "smoke-crd", "external": "keepme"}),
			"annotations": liveStringMap(map[string]string{"reviewed": "yes", "scraped": "true"}),
		},
	)
	got, ok := ManifestDeclaredKeys(applied)
	if !ok {
		t.Fatal("an applied manifest was not recognised")
	}
	if want := []string{"tier", markers.TagEstate}; !equalStrings(got[markers.LabelSurfaceAttr], want) {
		t.Errorf("labels = %v, want %v - sorted, the marker included, and nothing the live object added", got[markers.LabelSurfaceAttr], want)
	}
	if want := []string{"reviewed"}; !equalStrings(got[markers.AnnotationSurfaceAttr], want) {
		t.Errorf("annotations = %v, want %v", got[markers.AnnotationSurfaceAttr], want)
	}

	// A configuration with no annotations records an EMPTY annotation
	// set, not an absent one. That is what makes the last-key case
	// visible: the next apply's record has to be able to say "we declared
	// none" so that the one before it can be diffed against it.
	none := manifestReadValue(
		map[string]cty.Value{"labels": priorObjectMap(map[string]string{markers.TagEstate: "smoke-crd"})},
		map[string]cty.Value{"labels": liveStringMap(map[string]string{markers.TagEstate: "smoke-crd"})},
	)
	got, ok = ManifestDeclaredKeys(none)
	if !ok {
		t.Fatal("a manifest with no annotations was not recognised")
	}
	if list, has := got[markers.AnnotationSurfaceAttr]; !has || len(list) != 0 {
		t.Errorf("annotations = %v (present %v), want an empty set", list, has)
	}

	// Anything that is not the manifest shape is refused rather than
	// recorded as an empty declaration, which would read as "this
	// configuration declares no labels" on the next plan and propose
	// removing every one of them.
	if _, ok := ManifestDeclaredKeys(cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("i-1")})); ok {
		t.Error("a non-manifest object produced a declared key set")
	}
	if _, ok := ManifestDeclaredKeys(cty.NullVal(cty.EmptyObject)); ok {
		t.Error("a null object produced a declared key set")
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
