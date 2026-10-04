// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1883, measured by reference-k8s-platform-app's day2_crash.
// `live-mv -from-estate` moves a kubernetes_manifest ConfigMap into an
// estate whose block names its namespace through another resource
// (kubernetes_namespace.platform.metadata[0].name). The move rewrites the
// object's markers and writes no record in the destination, so the
// destination's next plan reads the object with no record: the sweep finds
// it by its estate label and address annotation and vouches for it
// (Ownership.Verified), and the seed is all that can give the prior a
// `manifest`. [partialManifestSeed] declined it, because an identity leaf
// (metadata.namespace) reads another resource, and the prior carried a null
// manifest - stock's own shape straight after `terraform import`. The
// provider then planned `+ manifest = {...}` as an in-place update and
// warned "Apply needed after 'import'": a completed move whose destination
// did not plan empty.
//
// For an instance the sweep verified, the resolver's import id already
// states which object it is, independently of the object read back, so the
// identity leaves configuration could not evaluate are taken from it. An
// unverified instance still declines (see
// TestManifestWithAnUnresolvableNameIsNotBound and the unverified arm here).
//
// Proving it red: pass "" for the import id at partialManifestSeed's call
// site in build.go and the verified arm fails with a null prior manifest.

const verifiedIdentityPair = `
resource "kubernetes_manifest" "ns" {
  manifest = { "apiVersion" = "v1", "kind" = "ConfigMap", "metadata" = { "name" = "ns", "namespace" = "x" } }
}

resource "kubernetes_manifest" "handoff" {
  manifest = {
    "apiVersion" = "v1"
    "kind"       = "ConfigMap"
    "metadata" = {
      "name"      = "handoff"
      "namespace" = kubernetes_manifest.ns.manifest.metadata.namespace
    }
    "data" = { "owner" = "platform" }
  }
}
`

func TestVerifiedManifestTakesAnUnresolvableIdentityFromItsImportID(t *testing.T) {
	addrNS := mustAddr(t, `kubernetes_manifest.ns`)
	addrH := mustAddr(t, `kubernetes_manifest.handoff`)
	build := func(t *testing.T, verified bool) (*Result, string) {
		t.Helper()
		cfg := refSeedConfig(t, verifiedIdentityPair)
		cluster := &refSeedCluster{}
		own := map[string]string{markers.TagEstate: refSeedEstate}
		idNS := cluster.put(refSeedLiveConfigMap("ns", own, nil, nil))
		// The object as a cross-estate move leaves it: this estate's label,
		// the address annotation, and nothing in this estate's records.
		idH := cluster.put(refSeedLiveConfigMap("handoff", own,
			map[string]string{markers.AddressAnnotation: addrH.String()},
			map[string]string{"owner": "platform"}))
		ownership := &Ownership{Estate: refSeedEstate}
		if verified {
			ownership.Verified = map[string]bool{addrNS.String(): true, addrH.String(): true}
		}
		provAddr, p := cluster.provider()
		res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
			{Addr: addrNS, Class: identity.ClassConcrete, ImportID: idNS},
			{Addr: addrH, Class: identity.ClassConcrete, ImportID: idH},
		}, SingleProvider(provAddr, p), Options{Ownership: ownership})
		assertNoErrors(t, diags)
		return res, idH
	}

	t.Run("verified", func(t *testing.T) {
		res, _ := build(t, true)
		if !res.Has(addrH) {
			om, _ := res.OmissionFor(addrH)
			t.Fatalf("the verified handoff is not in the prior state: %s %s", om.Reason, om.Detail)
		}
		want, _ := json.Marshal(map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			// The estate label and the address annotation, both as the
			// stamped configuration writes them; the namespace from the
			// import id.
			"metadata": map[string]any{
				"annotations": map[string]any{markers.AddressAnnotation: addrH.String()},
				"labels":      map[string]any{markers.TagEstate: refSeedEstate},
				"name":        "handoff",
				"namespace":   "x",
			},
			"data": map[string]any{"owner": "platform"},
		})
		if got := refSeedPriorManifest(t, res, addrH); got != string(want) {
			t.Errorf("handoff's prior manifest after a cross-estate move:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("unverified", func(t *testing.T) {
		res, idH := build(t, false)
		if res.Has(addrH) {
			t.Fatalf("an unverified manifest whose namespace configuration cannot state was bound to %s; the import id must only complete an identity the sweep vouched for", idH)
		}
		if om := omissionFor(t, res, addrH.String()); om.Reason != ReasonUnowned {
			t.Errorf("omitted as %s, want %s", om.Reason, ReasonUnowned)
		}
	})
}
