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
//  1. a label our field manager owns and the configuration no longer
//     declares enters the prior, which is what makes the removal plan;
//  2. a label some OTHER manager wrote never does, however live it is,
//     because mirroring it proposes a deletion the server undoes and the
//     plan then churns for ever.
//
// Arm 2 is the one the naive widening fails, so every case below that
// asserts a key is ABSENT is load-bearing: delete those assertions and the
// wholesale mirror passes this file.

// ownedSet is one [ManifestOwnedKeys].Keys, spelled the way the mirror
// reads it.
func ownedSet(labels, annotations []string) map[string]map[string]bool {
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

// TestMirrorManifestComputedFieldsPlansAnOwnedRemoval is arm 1. The
// configuration used to declare squad and owner and no longer does; the
// live object still has them; our manager wrote them. They must be in the
// prior, holding the LIVE value, so the provider sees the configuration
// differ from the prior and plans the removal.
func TestMirrorManifestComputedFieldsPlansAnOwnedRemoval(t *testing.T) {
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
	owned := ownedSet(
		[]string{"tier", markers.TagEstate, "squad"},
		[]string{"reviewed", "owner"},
	)

	// The negative control, and the whole reason this fix exists: with no
	// owned set, the removed keys are in neither the prior nor the
	// configuration, the two agree, and nothing plans.
	before := priorMapOf(t, mirrorManifestComputedFields(in, block, nil), "labels")
	if _, has := before["squad"]; has {
		t.Fatal("the pre-#1211 mirror already carried the removed key; this test proves nothing")
	}

	got := mirrorManifestComputedFields(in, block, owned)
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
// is the test the wholesale widening fails. Three keys the live object
// carries that our manager did not write: one from `kubectl label`, the
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
	// Our manager owns only the marker and the one annotation the
	// configuration declares - which is exactly what the API server says
	// when kubectl and a controller wrote the rest.
	owned := ownedSet([]string{markers.TagEstate}, []string{"reviewed"})

	got := mirrorManifestComputedFields(in, block, owned)
	labels := priorMapOf(t, got, "labels")
	if len(labels) != 1 || labels[markers.TagEstate] != "smoke-crd" {
		t.Errorf("labels = %v, want only the marker; anything else plans a deletion the server undoes", labels)
	}
	ann := priorMapOf(t, got, "annotations")
	if len(ann) != 1 || ann["reviewed"] != "yes" {
		t.Errorf("annotations = %v, want only the declared one", ann)
	}
}

// TestMirrorManifestComputedFieldsIgnoresAnOwnedKeyTheObjectHasNot: our
// manager's entry names a key some later writer has already removed. There
// is nothing to mirror and nothing to remove, and inventing a prior entry
// for it would propose deleting a key that is not there.
func TestMirrorManifestComputedFieldsIgnoresAnOwnedKeyTheObjectHasNot(t *testing.T) {
	block := manifestTypeSchema().Block
	in := manifestReadValue(
		map[string]cty.Value{"labels": priorObjectMap(map[string]string{markers.TagEstate: "smoke-crd"})},
		map[string]cty.Value{"labels": liveStringMap(map[string]string{markers.TagEstate: "smoke-crd"})},
	)
	got := mirrorManifestComputedFields(in, block, ownedSet([]string{markers.TagEstate, "gone"}, nil))
	if labels := priorMapOf(t, got, "labels"); len(labels) != 1 || labels[markers.TagEstate] != "smoke-crd" {
		t.Errorf("labels = %v, want only the marker", labels)
	}
	// And with nothing to change, the value comes back as it was.
	if !got.RawEquals(in) {
		t.Error("a no-op mirror rebuilt the value")
	}
}

// TestOwnedManifestKeysDegradation pins what happens when the answer
// cannot be had. Every one of these ends in today's narrow prior, which
// plans No changes over a removed key - so every one of them must SAY so.
// A silent nil here is GitHub issue #1211 restored.
func TestOwnedManifestKeysDegradation(t *testing.T) {
	live := manifestReadValue(
		map[string]cty.Value{"labels": priorObjectMap(map[string]string{markers.TagEstate: "smoke-crd"})},
		map[string]cty.Value{"labels": liveStringMap(map[string]string{markers.TagEstate: "smoke-crd"})},
	)
	addr := manifestAddr(t)

	warns := func(t *testing.T, lookup *manifestKeyLookup, want string) {
		t.Helper()
		keys, diags := ownedManifestKeys(context.Background(), live, lookup)
		if keys != nil {
			t.Errorf("keys = %v, want none", keys)
		}
		if len(diags) != 1 {
			t.Fatalf("diagnostics = %d, want exactly one warning", len(diags))
		}
		if got := diags[0].Description().Summary; got != SummaryManifestOwnedKeysUnavailable {
			t.Errorf("summary = %q, want %q", got, SummaryManifestOwnedKeysUnavailable)
		}
		if diags.HasErrors() {
			t.Error("a cluster that would not answer a supplementary read failed the whole estate")
		}
		if detail := diags[0].Description().Detail; want != "" && !strings.Contains(detail, want) {
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
			return ManifestOwnedKeys{Keys: ownedSet(nil, nil)}, nil
		}}, "no metadata.managedFields")
	})

	t.Run("answer names no maps", func(t *testing.T) {
		warns(t, &manifestKeyLookup{addr: addr, hook: func(context.Context, ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
			return ManifestOwnedKeys{ManagedFieldsPresent: true}, nil
		}}, "no key sets")
	})

	t.Run("not a manifest type", func(t *testing.T) {
		// Nil lookup is every AWS read in the corpus. It must cost
		// nothing at all - no round trip, and no warning either.
		keys, diags := ownedManifestKeys(context.Background(), live, nil)
		if keys != nil || len(diags) != 0 {
			t.Errorf("keys = %v, diags = %v, want neither", keys, diags)
		}
	})

	t.Run("our manager owns nothing, and that is an answer", func(t *testing.T) {
		// managedFields present, no entry of ours. That is exact, not a
		// failure: there is nothing of ours to remove, and warning here
		// would cry wolf on every adopted object.
		keys, diags := ownedManifestKeys(context.Background(), live, &manifestKeyLookup{addr: addr,
			hook: func(context.Context, ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
				return ManifestOwnedKeys{Keys: ownedSet(nil, nil), ManagedFieldsPresent: true}, nil
			}})
		if keys == nil {
			t.Fatal("an exact empty answer was reported as unavailable")
		}
		if len(diags) != 0 {
			t.Errorf("diagnostics = %v, want none", diags)
		}
	})
}

// TestOwnedManifestKeysNamesTheLiveObject: the request carries the object
// the PROVIDER read, off its own `object` attribute, because that is the
// object whose managedFields answer the question.
func TestOwnedManifestKeysNamesTheLiveObject(t *testing.T) {
	live := manifestReadValue(
		map[string]cty.Value{"labels": priorObjectMap(map[string]string{markers.TagEstate: "smoke-crd"})},
		map[string]cty.Value{"labels": liveStringMap(map[string]string{markers.TagEstate: "smoke-crd"})},
	)
	addr := manifestAddr(t)
	var saw ManifestOwnedKeysRequest
	_, diags := ownedManifestKeys(context.Background(), live, &manifestKeyLookup{addr: addr,
		hook: func(_ context.Context, req ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
			saw = req
			return ManifestOwnedKeys{Keys: ownedSet(nil, nil), ManagedFieldsPresent: true}, nil
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

// TestOwnedManifestKeysRefusesAnUnnameableObject: no apiVersion, kind and
// name means no object to ask about. It warns rather than asking about a
// half-named one.
func TestOwnedManifestKeysRefusesAnUnnameableObject(t *testing.T) {
	bare := cty.ObjectVal(map[string]cty.Value{
		markers.ManifestSurfaceAttr: cty.EmptyObjectVal,
		markers.ManifestLiveAttr:    cty.NullVal(cty.DynamicPseudoType),
	})
	called := false
	keys, diags := ownedManifestKeys(context.Background(), bare, &manifestKeyLookup{addr: manifestAddr(t),
		hook: func(context.Context, ManifestOwnedKeysRequest) (ManifestOwnedKeys, error) {
			called = true
			return ManifestOwnedKeys{}, nil
		}})
	if called {
		t.Error("the cluster was asked about an object with no name")
	}
	if keys != nil || len(diags) != 1 || diags[0].Description().Summary != SummaryManifestOwnedKeysUnavailable {
		t.Errorf("keys = %v, diags = %v, want one warning and no keys", keys, diags)
	}
}

// TestMirrorManifestComputedFieldsPlansTheLastKeyOfAMap: deleting the last
// annotation from a configuration deletes the `annotations` map with it, so
// the prior manifest has no such attribute to widen. The owned keys the
// object still carries go into a map of their own, or the removal is
// invisible - which is how the smoke's own step 6 first failed, on the
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

	// Nothing owned there: the attribute stays absent, because absent on
	// both sides is agreement and there is nothing to remove.
	if got := mirrorManifestComputedFields(in, block, ownedSet([]string{markers.TagEstate}, nil)); !got.RawEquals(in) {
		t.Error("a map the configuration never had and we own nothing in was invented")
	}

	got := mirrorManifestComputedFields(in, block, ownedSet([]string{markers.TagEstate}, []string{"reviewed"}))
	ann := priorMapOf(t, got, "annotations")
	if len(ann) != 1 || ann["reviewed"] != "yes" {
		t.Fatalf("annotations = %v, want only the owned key at its live value", ann)
	}
	if _, has := ann["kubectl.kubernetes.io/last-applied-configuration"]; has {
		t.Error("a foreign manager's annotation entered a prior map built from nothing")
	}
}
