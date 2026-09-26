// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestEverySubstrateAnswersEveryQuestion is the contract's shape: each
// family owns its surfaces alone, says how each is written, and names a
// sweep. "Every substrate has a sweep client" is the property the
// completeness guard cannot see (the Kubernetes leg is chosen by provider,
// not by a surface predicate), so it is asserted here.
func TestEverySubstrateAnswersEveryQuestion(t *testing.T) {
	owner := map[markers.Surface]string{}
	for _, s := range All {
		if got, ok := ForProvider(s.Name()); !ok || got != s {
			t.Errorf("ForProvider(%q) = %v, %v; want the %s substrate", s.Name(), got, ok, s.Name())
		}
		if s.Sweep() == "" {
			t.Errorf("%s names no sweep", s.Name())
		}
		if len(s.Surfaces()) == 0 {
			t.Errorf("%s carries no surface", s.Name())
		}
		for _, surface := range s.Surfaces() {
			if prev, dup := owner[surface]; dup {
				t.Errorf("surface %q belongs to both %s and %s", surface, prev, s.Name())
			}
			owner[surface] = s.Name()
			if For(surface) != s {
				t.Errorf("For(%q) is not %s", surface, s.Name())
			}
			w := WritesOf(surface)
			if w.Create == "" || w.Adopt == "" {
				t.Errorf("%s surface %q: writes %+v, want both occasions named", s.Name(), surface, w)
			}
			if CarriesAddress(surface) != s.CarriesAddress() {
				t.Errorf("CarriesAddress(%q) disagrees with %s", surface, s.Name())
			}
		}
	}
	if For("") != nil || CarriesAddress("") || WritesOf("") != (Writes{}) {
		t.Error("the zero Surface belongs to a family")
	}
	if _, ok := MarkersOf("", cty.EmptyObjectVal); ok {
		t.Error("MarkersOf the zero Surface read a map")
	}
	if _, ok := ForProvider("google"); ok {
		t.Error("ForProvider(\"google\") found a family; none is written (#1118's ruling)")
	}
}

// TestSweepClientByFamily: the AWS sweep is the provider's own, so no
// client is built from its block; the Kubernetes one is built from the
// block, and a block with no reachable cluster is an error rather than a
// silent nil.
func TestSweepClientByFamily(t *testing.T) {
	if c, err := AWS.NewSweeper(cty.NilVal, false); c != nil || err != nil {
		t.Errorf("AWS.NewSweeper = %v, %v; want nil, nil", c, err)
	}
	if AWS.Sweep() != SweepTaggingIndex || Kubernetes.Sweep() != SweepLabelList {
		t.Errorf("sweeps: aws %q, kubernetes %q", AWS.Sweep(), Kubernetes.Sweep())
	}
	t.Setenv("KUBECONFIG", t.TempDir()+"/none")
	t.Setenv("HOME", t.TempDir())
	if c, err := Kubernetes.NewSweeper(cty.NilVal, false); c != nil && err == nil {
		t.Error("Kubernetes.NewSweeper built a client from no configuration at all")
	}
}

func tagsBlock(optional, computed bool) *configschema.Block {
	return &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"tags": {Type: cty.Map(cty.String), Optional: optional, Computed: computed},
	}}
}

func labelsBlock() *configschema.Block {
	return &configschema.Block{BlockTypes: map[string]*configschema.NestedBlock{
		"metadata": {
			Nesting:  configschema.NestingList,
			MinItems: 1,
			MaxItems: 1,
			Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"labels": {Type: cty.Map(cty.String), Optional: true},
			}},
		},
	}}
}

func manifestBlock() *configschema.Block {
	return &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"manifest": {Type: cty.DynamicPseudoType, Required: true},
		"object":   {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
	}}
}

// TestSurfaceOfAndOwnershipSurfaceOf pins both questions, including the
// one place they differ: a tags attribute the configuration cannot set is
// an ownership surface (the projection has always read it) and not a
// surface a marker is written to (live-mv and live-import never wrote
// one). The extraction kept each caller on the question it asked.
func TestSurfaceOfAndOwnershipSurfaceOf(t *testing.T) {
	cases := map[string]struct {
		block          *configschema.Block
		surface, owner markers.Surface
	}{
		"nil":                {nil, "", ""},
		"settable tags":      {tagsBlock(true, true), markers.SurfaceTags, markers.SurfaceTags},
		"computed-only tags": {tagsBlock(false, true), "", markers.SurfaceTags},
		"metadata labels":    {labelsBlock(), markers.SurfaceLabels, markers.SurfaceLabels},
		"manifest":           {manifestBlock(), markers.SurfaceManifest, markers.SurfaceManifest},
		"no surface":         {&configschema.Block{}, "", ""},
	}
	for name, tc := range cases {
		got, ok := SurfaceOf(tc.block)
		if got != tc.surface || ok != (tc.surface != "") {
			t.Errorf("%s: SurfaceOf = %q, %v; want %q", name, got, ok, tc.surface)
		}
		got, ok = OwnershipSurfaceOf(tc.block)
		if got != tc.owner || ok != (tc.owner != "") {
			t.Errorf("%s: OwnershipSurfaceOf = %q, %v; want %q", name, got, ok, tc.owner)
		}
		if tc.surface != "" {
			if fam, ok := For(tc.surface).SurfaceOf(tc.block); !ok || fam != tc.surface {
				t.Errorf("%s: the owning family's SurfaceOf = %q, %v", name, fam, ok)
			}
			for _, other := range All {
				if other == For(tc.surface) {
					continue
				}
				if _, ok := other.SurfaceOf(tc.block); ok {
					t.Errorf("%s: %s also claims it", name, other.Name())
				}
			}
		}
	}
}

func TestMarkersOfReadsEachSurface(t *testing.T) {
	tags := cty.ObjectVal(map[string]cty.Value{
		"tags": cty.MapVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("e")}),
	})
	labels := cty.ObjectVal(map[string]cty.Value{
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"labels": cty.MapVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("e")}),
		})}),
	})
	manifest := cty.ObjectVal(map[string]cty.Value{
		"manifest": cty.ObjectVal(map[string]cty.Value{
			"metadata": cty.ObjectVal(map[string]cty.Value{
				"labels": cty.ObjectVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("e")}),
			}),
		}),
	})
	for surface, obj := range map[markers.Surface]cty.Value{
		markers.SurfaceTags:     tags,
		markers.SurfaceLabels:   labels,
		markers.SurfaceManifest: manifest,
	} {
		got, ok := MarkersOf(surface, obj)
		if !ok || got[markers.TagEstate] != "e" {
			t.Errorf("MarkersOf(%q) = %v, %v; want tofu-estate=e", surface, got, ok)
		}
	}
}
