// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
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
			if w.Create == "" || w.Adopt == "" || w.PostCreate == "" {
				t.Errorf("%s surface %q: writes %+v, want every occasion named", s.Name(), surface, w)
			}
			// GitHub issue #1587: a surface's post-create write is either
			// never needed or the one its family's provider
			// configurations build a writer for.
			if w.PostCreate != WriteNeverNeeded && w.PostCreate != s.MarkerWriter(addrs.AbsProviderConfig{}) {
				t.Errorf("%s surface %q: post-create write %q, but the family's provider configurations build %q", s.Name(), surface, w.PostCreate, s.MarkerWriter(addrs.AbsProviderConfig{}))
			}
			if CarriesAddress(surface) != s.CarriesAddress() {
				t.Errorf("CarriesAddress(%q) disagrees with %s", surface, s.Name())
			}
			if AddressInMarkers(surface) != s.AddressInMarkers() {
				t.Errorf("AddressInMarkers(%q) disagrees with %s", surface, s.Name())
			}
			// GitHub issue #1641: an address in the marker map is an
			// address carried; the converse is Kubernetes' annotation.
			if AddressInMarkers(surface) && !CarriesAddress(surface) {
				t.Errorf("%s surface %q holds tofu-address in its marker map but says it carries no address", s.Name(), surface)
			}
			if CarrierPhrase(surface) == "" {
				t.Errorf("%s surface %q has no carrier phrase, so a missing marker map on it would be reported without saying which map", s.Name(), surface)
			}
		}
	}
	if For("") != nil || CarriesAddress("") || AddressInMarkers("") || WritesOf("") != (Writes{}) || CreateCollidesOnKey("") || CarrierPhrase("") != "" {
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
	// Both halves, separately (#1741): the old `c != nil && err == nil`
	// passed on (nil, nil), exactly the silent nil the doc comment names.
	c, err := Kubernetes.NewSweeper(cty.NilVal, false)
	if c != nil {
		t.Errorf("Kubernetes.NewSweeper built a client (%v) from no configuration at all", c)
	}
	if err == nil {
		t.Error("Kubernetes.NewSweeper returned no error for a block with no reachable cluster; a nil client with a nil error is the silent nil this test exists to refuse")
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

// TestSurfaceOf pins the one question every caller now asks, including the
// dispatch package: live-mv's surface switch, live-import's carrier choice,
// and (GitHub issue #1589) the projection's ownership read all ask this
// same [SurfaceOf], not a second, looser question of their own.
func TestSurfaceOf(t *testing.T) {
	cases := map[string]struct {
		block   *configschema.Block
		surface markers.Surface
	}{
		"nil":                {nil, ""},
		"settable tags":      {tagsBlock(true, true), markers.SurfaceTags},
		"computed-only tags": {tagsBlock(false, true), ""},
		"metadata labels":    {labelsBlock(), markers.SurfaceLabels},
		"manifest":           {manifestBlock(), markers.SurfaceManifest},
		"no surface":         {&configschema.Block{}, ""},
	}
	for name, tc := range cases {
		got, ok := SurfaceOf(tc.block)
		if got != tc.surface || ok != (tc.surface != "") {
			t.Errorf("%s: SurfaceOf = %q, %v; want %q", name, got, ok, tc.surface)
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

// TestSurfaceWording pins GitHub issue #1584's answers, which the
// projection's own surface enum held before: the carrier each refusal names,
// the one surface whose create collides on its key (#1546), and the
// no-carrier sentence in each family's words.
func TestSurfaceWording(t *testing.T) {
	for surface, want := range map[markers.Surface]struct {
		phrase   string
		collides bool
	}{
		markers.SurfaceTags:     {"tags attribute", false},
		markers.SurfaceLabels:   {"metadata.labels map", true},
		markers.SurfaceManifest: {"manifest.metadata.labels map", false},
	} {
		if got := CarrierPhrase(surface); got != want.phrase {
			t.Errorf("CarrierPhrase(%q) = %q, want %q", surface, got, want.phrase)
		}
		if got := CreateCollidesOnKey(surface); got != want.collides {
			t.Errorf("CreateCollidesOnKey(%q) = %v, want %v", surface, got, want.collides)
		}
	}

	block := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name": {Type: cty.String, Required: true},
	}}
	for provider, want := range map[string]string{
		"aws":        "aws_thing has no tags map this configuration can set, so there is nowhere to carry an ownership marker.",
		"kubernetes": "kubernetes_labels has no metadata.labels map and no manifest.metadata.labels map, so there is nowhere on it to carry an ownership marker.",
		"google":     "google_thing is from a provider this fork has no marker surface for, so there is nowhere to carry an ownership marker.",
	} {
		typeName := map[string]string{"aws": "aws_thing", "kubernetes": "kubernetes_labels", "google": "google_thing"}[provider]
		if got := NotACarrier(provider, block, typeName); got != want {
			t.Errorf("NotACarrier(%q):\n got %q\nwant %q", provider, got, want)
		}
	}
}

// TestPostCreateWrites pins #1587's answers: AWS marks a type whose create
// call cannot carry tags through the Tagging API, and Kubernetes never
// needs a post-create write because the label rides the create.
func TestPostCreateWrites(t *testing.T) {
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}
	if got := WritesOf(markers.SurfaceTags).PostCreate; got != WriteTaggingAPI {
		t.Errorf("tags surface post-create write %q, want %q", got, WriteTaggingAPI)
	}
	if got := AWS.MarkerWriter(provider); got != WriteTaggingAPI {
		t.Errorf("AWS marker writer %q, want %q", got, WriteTaggingAPI)
	}
	for _, surface := range Kubernetes.Surfaces() {
		if got := WritesOf(surface).PostCreate; got != WriteNeverNeeded {
			t.Errorf("%s surface post-create write %q, want %q", surface, got, WriteNeverNeeded)
		}
	}
	if got := Kubernetes.MarkerWriter(provider); got != WriteNeverNeeded {
		t.Errorf("Kubernetes marker writer %q, want %q", got, WriteNeverNeeded)
	}
}

// fakeManualMarkFacts is an [AWSCreateTagFacts] this test controls directly,
// so AWS's [Substrate.ManualMarkFix] can be pinned without a real
// registry.Roster.
type fakeManualMarkFacts struct {
	cfnType string
	known   bool
}

func (f fakeManualMarkFacts) CloudControlTypeOrService(string) (string, bool) {
	return f.cfnType, f.known
}
func (fakeManualMarkFacts) TagsAfterCreate(string) bool { return true }

// TestManualMarkFix_awsKeepsTodaysThreeBranches pins GitHub issue #1084's
// wording byte-for-byte across its three branches - an ARN takes the
// Tagging API command, no ARN but a known CloudFormation type names its
// own tag write, neither names only the markers - now asked through
// [Substrate.ManualMarkFix] instead of built inline by the projection.
func TestManualMarkFix_awsKeepsTodaysThreeBranches(t *testing.T) {
	want := map[string]string{"tofu-estate": "prod"}
	tagsArg := markers.TagsArgument(want)
	withARN := createdAfterThing(cty.StringVal("arn:aws:after:::thing/T1"))
	noARN := createdAfterThing(cty.NullVal(cty.String))
	awsFacts := func(f AWSCreateTagFacts) Facts { return Facts{AWS.Name(): f} }

	if got, want := ManualMarkFix(markers.SurfaceTags, withARN, want, awsFacts(fakeManualMarkFacts{cfnType: "AWS::After::Thing", known: true})),
		"Mark it, then plan again:\n\n  aws resourcegroupstaggingapi tag-resources --resource-arn-list arn:aws:after:::thing/T1 --tags "+tagsArg; got != want {
		t.Errorf("with an arn:\n got %q\nwant %q", got, want)
	}
	if got, want := ManualMarkFix(markers.SurfaceTags, noARN, want, awsFacts(fakeManualMarkFacts{cfnType: "AWS::After::Thing", known: true})),
		"Mark it by hand with the tag write AWS::After::Thing takes, with the tags "+tagsArg+", then plan again."; got != want {
		t.Errorf("no arn, known CFN type:\n got %q\nwant %q", got, want)
	}
	if got, want := ManualMarkFix(markers.SurfaceTags, noARN, want, awsFacts(fakeManualMarkFacts{known: false})),
		"Mark it by hand with the markers "+tagsArg+", then plan again."; got != want {
		t.Errorf("no arn, no CFN type:\n got %q\nwant %q", got, want)
	}
	if got, want := ManualMarkFix(markers.SurfaceTags, noARN, want, nil),
		"Mark it by hand with the markers "+tagsArg+", then plan again."; got != want {
		t.Errorf("no arn, nil facts:\n got %q\nwant %q", got, want)
	}
}

// createdAfterThing is aws_after_thing.x as created, with arn as its arn
// attribute.
func createdAfterThing(arn cty.Value) Created {
	return Created{
		Addr: addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_after_thing", Name: "x"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance),
		Object: cty.ObjectVal(map[string]cty.Value{
			"id":  cty.NullVal(cty.String),
			"arn": arn,
		}),
	}
}

// TestManualMarkFix_zeroSurfaceIsGeneric: a surface with no family gets the
// generic sentence naming only the markers, since there is no family to
// name a command.
func TestManualMarkFix_zeroSurfaceIsGeneric(t *testing.T) {
	want := map[string]string{"tofu-estate": "prod"}
	got := ManualMarkFix("", createdAfterThing(cty.StringVal("some-arn")), want, nil)
	if wantStr := "Mark it by hand with the markers " + markers.TagsArgument(want) + ", then plan again."; got != wantStr {
		t.Errorf("zero surface: got %q, want %q", got, wantStr)
	}
}

// TestEveryFamilyCarriesTheAddress is GitHub issue #1641's flip, pinned
// per surface: both families carry the block address (#1605's ruling), and
// only the AWS tag map holds it as a marker key.
func TestEveryFamilyCarriesTheAddress(t *testing.T) {
	for _, tc := range []struct {
		surface           markers.Surface
		carries, inTheMap bool
	}{
		{markers.SurfaceTags, true, true},
		{markers.SurfaceLabels, true, false},
		{markers.SurfaceManifest, true, false},
	} {
		if got := CarriesAddress(tc.surface); got != tc.carries {
			t.Errorf("CarriesAddress(%q) = %v, want %v", tc.surface, got, tc.carries)
		}
		if got := AddressInMarkers(tc.surface); got != tc.inTheMap {
			t.Errorf("AddressInMarkers(%q) = %v, want %v", tc.surface, got, tc.inTheMap)
		}
	}
}
