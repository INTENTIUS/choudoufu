// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1742 items 4 and 7: the seams a third family would fall
// into without anything going red. Each test here registers a fake family
// beside the real two and asks the package-level dispatch, so a dispatch
// that routes the fake's types to AWS (or AWS's to the fake) fails.

// surfaceFakeTags is the fake family's own surface. Surfaces are unique to
// a family (TestEverySubstrateAnswersEveryQuestion), so a third family
// whose marker is a tags map names a surface of its own.
const surfaceFakeTags markers.Surface = "fake-tags"

// tagsMapFamily is a third family whose marker is a settable top-level
// tags map, azurerm's shape: on the schema alone it is indistinguishable
// from an AWS type. Everything it does not override is AWS's, by
// embedding, the way the controller-held fakes are built.
type tagsMapFamily struct {
	Substrate
	name string
	// synthesized counts SynthesizeIdentity calls.
	synthesized *int
}

func newTagsMapFamily(name string) tagsMapFamily {
	return tagsMapFamily{Substrate: AWS, name: name, synthesized: new(int)}
}

func (f tagsMapFamily) Name() string              { return f.name }
func (tagsMapFamily) Surfaces() []markers.Surface { return []markers.Surface{surfaceFakeTags} }
func (tagsMapFamily) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if markers.Taggable(block) {
		return surfaceFakeTags, true
	}
	return "", false
}
func (f tagsMapFamily) NotACarrier(_ *configschema.Block, typeName string) string {
	return fmt.Sprintf("%s: the %s family's own sentence", typeName, f.name)
}

// SynthesizeIdentity claims a type of the fake's own shape only: a tags
// map. It is never the catch-all.
func (f tagsMapFamily) SynthesizeIdentity(_ string, schema providers.Schema) (SynthesizedIdentity, bool) {
	*f.synthesized++
	if markers.Taggable(schema.Block) {
		return SynthesizedIdentity{NonAWSProvider: true, ImportSyntax: "ID"}, true
	}
	return SynthesizedIdentity{}, false
}

// withFamilies replaces [All] for one test.
func withFamilies(t *testing.T, all ...Substrate) {
	t.Helper()
	orig := All
	All = all
	t.Cleanup(func() { All = orig })
}

// familiesNeverAsked names the families of all that an ordered question
// can never reach: every family after the first one whose
// [Substrate.SynthesizeIdentity] claims a schema with no shape at all.
// Such a family is a catch-all (AWS's identity-schema route claims every
// type), and the dispatch stops at the first answer.
func familiesNeverAsked(all []Substrate) []string {
	for i, s := range all {
		if _, claims := s.SynthesizeIdentity("probe_no_shape", providers.Schema{}); !claims {
			continue
		}
		var never []string
		for _, after := range all[i+1:] {
			never = append(never, after.Name())
		}
		return never
	}
	return nil
}

// TestTheCatchAllFamilyIsLast (GitHub issue #1742 item 4): a family
// appended to [All] after AWS is never asked how its types are identified,
// because AWS claims every type. Nothing held that order but a comment.
func TestTheCatchAllFamilyIsLast(t *testing.T) {
	if never := familiesNeverAsked(All); len(never) > 0 {
		t.Errorf("substrate.All asks a catch-all family before %s, so no type of theirs is ever asked of them; a catch-all (AWS's identity-schema route) must be last", strings.Join(never, ", "))
	}
}

// TestAFamilyAfterTheCatchAllIsNeverAsked is the hazard item 4's guard
// names, shown on a fake: appended after AWS, a family's types reach AWS's
// answer and its own SynthesizeIdentity is never called. Placed before
// AWS, it answers for its own shape.
func TestAFamilyAfterTheCatchAllIsNeverAsked(t *testing.T) {
	schema := providers.Schema{Block: tagsBlock(true, true)}

	after := newTagsMapFamily("fakeafter")
	withFamilies(t, Kubernetes, AWS, after)
	if never := familiesNeverAsked(All); len(never) != 1 || never[0] != "fakeafter" {
		t.Errorf("familiesNeverAsked = %v, want [fakeafter]", never)
	}
	*after.synthesized = 0
	synth, ok := SynthesizeIdentity("fake_thing", schema)
	if !ok || !synth.FromIdentitySchema || *after.synthesized != 0 {
		t.Errorf("after AWS: SynthesizeIdentity = %+v, %v with the fake asked %d times; want AWS's identity-schema answer and the fake never asked", synth, ok, *after.synthesized)
	}

	before := newTagsMapFamily("fakebefore")
	withFamilies(t, Kubernetes, before, AWS)
	if never := familiesNeverAsked(All); len(never) != 0 {
		t.Errorf("familiesNeverAsked = %v, want none", never)
	}
	*before.synthesized = 0
	synth, ok = SynthesizeIdentity("fake_thing", schema)
	if !ok || synth.FromIdentitySchema || *before.synthesized != 1 {
		t.Errorf("before AWS: SynthesizeIdentity = %+v, %v with the fake asked %d times; want the fake's own answer", synth, ok, *before.synthesized)
	}
}

// TestSurfaceDispatchIsByProvider (GitHub issue #1742 item 7): a third
// family whose marker is a tags map (azurerm's shape) is told apart from
// AWS by provider, the rule [NotACarrier] already used. Before, the
// surface questions dispatched on the schema alone, so wherever the fake
// sat in [All] one of the two families was handed the other's types, and
// [For] on the surface read disagreed with [NotACarrier] about which
// family answers for the provider.
func TestSurfaceDispatchIsByProvider(t *testing.T) {
	tagged, untagged := tagsBlock(true, true), &configschema.Block{}
	for _, placement := range []string{"before AWS", "after AWS"} {
		t.Run(placement, func(t *testing.T) {
			fake := newTagsMapFamily("azurerm")
			if placement == "before AWS" {
				withFamilies(t, Kubernetes, fake, AWS)
			} else {
				withFamilies(t, Kubernetes, AWS, fake)
			}
			for _, tc := range []struct {
				provider string
				want     Substrate
			}{
				{"aws", AWS},
				{"azurerm", fake},
			} {
				surface, ok := SurfaceOf(tc.provider, tagged)
				if !ok {
					t.Errorf("provider %s: a settable tags map carries no surface", tc.provider)
					continue
				}
				if got := For(surface); got == nil || got.Name() != tc.want.Name() {
					t.Errorf("provider %s: the tags map's surface %q belongs to %v; want %s", tc.provider, surface, nameOf(got), tc.want.Name())
				}
				if got, want := NotACarrier(tc.provider, untagged, "x_thing"), tc.want.NotACarrier(untagged, "x_thing"); got != want {
					t.Errorf("provider %s: NotACarrier = %q; want %s's own %q, the family the surface read chose", tc.provider, got, tc.want.Name(), want)
				}
			}
		})
	}
}

func nameOf(s Substrate) string {
	if s == nil {
		return "no family"
	}
	return s.Name()
}
