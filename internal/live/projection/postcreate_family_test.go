// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1742, items 1 and 2: the post-create path's holes a third
// family would fall into. Neither fires on AWS or Kubernetes, so each is
// made to fire by a fake family registered ahead of the real ones.

// withFamilyFirst registers f ahead of every real family for the test, so
// [substrate.For] and [substrate.SurfaceOf] ask it first.
func withFamilyFirst(t *testing.T, f substrate.Substrate) {
	t.Helper()
	saved := substrate.All
	substrate.All = append([]substrate.Substrate{f}, saved...)
	t.Cleanup(func() { substrate.All = saved })
}

// silentFamily (item 1) says a create of its types cannot carry the marker,
// and then names no write to put it there afterwards: its surface's
// Writes.PostCreate and its MarkerWriter both answer WriteNeverNeeded. It
// claims the tags surface, the shape azurerm's types would arrive in.
type silentFamily struct{ substrate.Substrate }

func (silentFamily) Name() string                { return "silent" }
func (silentFamily) Surfaces() []markers.Surface { return []markers.Surface{markers.SurfaceTags} }
func (silentFamily) MarkerWriter() substrate.Write {
	return substrate.WriteNeverNeeded
}
func (silentFamily) Writes(surface markers.Surface) substrate.Writes {
	if surface == markers.SurfaceTags {
		return substrate.Writes{Create: substrate.WriteInCreate, Adopt: substrate.WriteTagsPlan, PostCreate: substrate.WriteNeverNeeded}
	}
	return substrate.Writes{}
}
func (silentFamily) PostCreateNeeded(surface markers.Surface, created substrate.Created, _ substrate.Facts) (string, bool) {
	if surface == markers.SurfaceTags && strings.HasPrefix(created.Type(), "silent_") {
		return created.Type() + " cannot take its marker in the create call (silent rules)", true
	}
	return "", false
}

// TestPostCreate_aWithheldCreateWithNoWriterIsRefusedBeforeTheCreate: a
// family that withholds the marker from the create and names no write to
// put it there afterwards would create an object nobody owns. The create
// side refuses at plan, an error, rather than withholding silently.
func TestPostCreate_aWithheldCreateWithNoWriterIsRefusedBeforeTheCreate(t *testing.T) {
	withFamilyFirst(t, silentFamily{substrate.AWS})

	n := &NodeResolver{Estate: "prod"}
	thing := locatedTestAddr(t, "silent_thing", "x")
	got, diags := n.AdjustCreateConfigValue(context.Background(), thing, tocConfig(cty.NullVal(cty.Map(cty.String))), tocSchema())
	t.Logf("create withheld: tags null = %v, diags=%d", got.GetAttr("tags").IsNull(), len(diags))
	if len(diags) != 1 || diags[0].Severity() != tfdiags.Error || diags[0].Description().Summary != "Object would be created without its marker" {
		t.Fatalf("the create withheld its markers with no post-create write to replace them, and nothing refused at plan: %v", diags)
	}
	detail := diags[0].Description().Detail
	for _, want := range []string{"silent_thing.x", "silent_thing cannot take its marker in the create call (silent rules)", `"never-needed"`, "silent"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, detail)
		}
	}

	// A type the family does not withhold for is stamped exactly as before.
	other := locatedTestAddr(t, "plain_thing", "x")
	stamped, diags := n.AdjustCreateConfigValue(context.Background(), other, tocConfig(cty.NullVal(cty.Map(cty.String))), tocSchema())
	if diags.HasErrors() {
		t.Fatalf("an ordinary create was refused: %v", diags.Err())
	}
	if tags := stamped.GetAttr("tags"); tags.IsNull() || tags.AsValueMap()[markers.TagEstate].AsString() != "prod" {
		t.Errorf("an ordinary create was not stamped: %#v", tags)
	}
}

// TestPostCreate_aWithheldCreateWithNoWriterFailsAfterTheCreate: the write
// side, reached by a create the plan-side refusal did not see (an apply of
// a plan made by another build, say), fails the apply naming the object
// instead of returning silently on WriteNeverNeeded.
func TestPostCreate_aWithheldCreateWithNoWriterFailsAfterTheCreate(t *testing.T) {
	withFamilyFirst(t, silentFamily{substrate.AWS})

	tagger := &fakeTagger{}
	n := &NodeResolver{Estate: "prod"}
	n.MarkerWriter = func(_ addrs.AbsProviderConfig, write substrate.Write) (MarkerWriter, error) {
		tagger.writes = append(tagger.writes, write)
		return TaggingAPIWriter{Tagger: tagger}, nil
	}
	thing := locatedTestAddr(t, "silent_thing", "x")
	_, diags := n.WriteAppliedMarkers(context.Background(), thing, tocProvider(), plans.Create, tocApplied("arn:silent:1", "S1", nil), tocSchema())
	t.Logf("post-create: diags=%d tagger calls=%d", len(diags), len(tagger.calls))
	if !diags.HasErrors() {
		t.Fatal("a create whose markers were withheld returned with no write and no error")
	}
	if d := diags[0].Description(); d.Summary != SummaryMarkerNotWritten || !strings.Contains(d.Detail, "silent_thing.x") || !strings.Contains(d.Detail, `"never-needed"`) {
		t.Errorf("the failure does not name the object and the missing write:\n%s\n%s", d.Summary, d.Detail)
	}
}

// bareSurface is item 2's family's surface.
const bareSurface markers.Surface = "bare-labels-surface"

// bareFamily (item 2) keeps its marker in a "labels" map and a nested
// meta[0].labels, never in tags or tags_all, and carries no block address
// in its marker map. Its objects also have a tags attribute, which belongs
// to nothing here and must not be written.
type bareFamily struct{ substrate.Substrate }

func (bareFamily) Name() string                  { return "bare" }
func (bareFamily) Surfaces() []markers.Surface   { return []markers.Surface{bareSurface} }
func (bareFamily) CarriesAddress() bool          { return false }
func (bareFamily) AddressInMarkers() bool        { return false }
func (bareFamily) MarkerWriter() substrate.Write { return "bare-write" }
func (bareFamily) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if _, ok := block.Attributes["labels"]; ok {
		return bareSurface, true
	}
	return "", false
}
func (bareFamily) Writes(surface markers.Surface) substrate.Writes {
	if surface == bareSurface {
		return substrate.Writes{Create: substrate.WriteInCreate, Adopt: "bare-write", PostCreate: "bare-write"}
	}
	return substrate.Writes{}
}
func (bareFamily) PostCreateNeeded(surface markers.Surface, created substrate.Created, _ substrate.Facts) (string, bool) {
	if surface == bareSurface {
		return created.Type() + " labels after the create", true
	}
	return "", false
}
func (bareFamily) CarrierPaths(surface markers.Surface) []cty.Path {
	if surface == bareSurface {
		return []cty.Path{cty.GetAttrPath("labels"), cty.GetAttrPath("meta").IndexInt(0).GetAttr("labels")}
	}
	return nil
}
func (bareFamily) CreatedObject(created substrate.Created) string { return created.Addr.String() }

func bareSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":     {Type: cty.String, Computed: true},
			"labels": {Type: cty.Map(cty.String), Optional: true},
			"tags":   {Type: cty.Map(cty.String), Optional: true},
			"meta":   {Type: cty.List(cty.Object(map[string]cty.Type{"labels": cty.Map(cty.String)})), Computed: true},
		},
	}}
}

func bareApplied() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":     cty.StringVal("B1"),
		"labels": cty.MapVal(map[string]cty.Value{"team": cty.StringVal("platform")}),
		"tags":   cty.NullVal(cty.Map(cty.String)),
		"meta": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"labels": cty.NullVal(cty.Map(cty.String)),
		})}),
	})
}

// TestPostCreate_theWriteBackIsTheFamilysShape: the markers handed to the
// writer are the family's marker map (no tofu-address where the address is
// not a key of it), and the stored object carries them where the family
// keeps its marker, not in AWS's tags and tags_all.
func TestPostCreate_theWriteBackIsTheFamilysShape(t *testing.T) {
	withFamilyFirst(t, bareFamily{substrate.AWS})

	writer := &fakeObjectWriter{}
	n := &NodeResolver{Estate: "prod"}
	n.MarkerWriter = func(addrs.AbsProviderConfig, substrate.Write) (MarkerWriter, error) { return writer, nil }

	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("bare")}
	thing := locatedTestAddr(t, "bare_thing", "x")
	stored, diags := n.WriteAppliedMarkers(context.Background(), thing, provider, plans.Create, bareApplied(), bareSchema())
	if diags.HasErrors() {
		t.Fatalf("write failed: %v", diags.Err())
	}
	if len(writer.calls) != 1 {
		t.Fatalf("want exactly 1 write, got %d", len(writer.calls))
	}
	if got := writer.calls[0].tags; got[markers.TagEstate] != "prod" || len(got) != 1 {
		t.Errorf("writer got markers %v, want only %s=prod: the family's marker map holds no address", got, markers.TagEstate)
	}

	labels := stored.GetAttr("labels")
	if mapEntry(labels, "team") != "platform" || mapEntry(labels, markers.TagEstate) != "prod" {
		t.Errorf("stored labels = %#v, want team=platform and the written marker", labels)
	}
	nested := stored.GetAttr("meta").Index(cty.NumberIntVal(0)).GetAttr("labels")
	if mapEntry(nested, markers.TagEstate) != "prod" {
		t.Errorf("stored meta[0].labels = %#v, want the written marker", nested)
	}
	if tags := stored.GetAttr("tags"); !tags.IsNull() {
		t.Errorf("the write-back wrote AWS's tags map on a family that keeps no marker there: %#v", tags)
	}
}

// TestPostCreate_anAddressOutsideTheMarkerMapIsNotSilentlyDropped: a family
// that carries its block address but not in its marker map (Kubernetes'
// annotation shape) has nowhere the post-create write could put it, so a
// create that needs that write is refused rather than written without one.
func TestPostCreate_anAddressOutsideTheMarkerMapIsNotSilentlyDropped(t *testing.T) {
	withFamilyFirst(t, annotatedBareFamily{bareFamily{substrate.AWS}})

	writer := &fakeObjectWriter{}
	n := &NodeResolver{Estate: "prod"}
	n.MarkerWriter = func(addrs.AbsProviderConfig, substrate.Write) (MarkerWriter, error) { return writer, nil }
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("bare")}
	_, diags := n.WriteAppliedMarkers(context.Background(), locatedTestAddr(t, "bare_thing", "x"), provider, plans.Create, bareApplied(), bareSchema())
	if !diags.HasErrors() {
		t.Fatalf("the address was dropped silently: %d writes, nothing refused", len(writer.calls))
	}
	if !strings.Contains(diags[0].Description().Detail, "bare-address") {
		t.Errorf("the failure does not name where the address lives:\n%s", diags[0].Description().Detail)
	}
}

// mapEntry is m[key] for a known, non-null map(string), or "".
func mapEntry(m cty.Value, key string) string {
	if m.IsNull() || !m.IsKnown() || !m.Type().IsMapType() {
		return ""
	}
	v, ok := m.AsValueMap()[key]
	if !ok || v.IsNull() {
		return ""
	}
	return v.AsString()
}

type annotatedBareFamily struct{ bareFamily }

func (annotatedBareFamily) CarriesAddress() bool { return true }
func (annotatedBareFamily) AddressCarrier(markers.Surface) (key, noun string) {
	return "bare-address", "annotation"
}

// TestPostCreate_theThreeAnswersAgree walks every family in substrate.All
// and every surface it owns, and holds the three answers to the
// post-create question together: Writes.PostCreate (which write a surface
// names), the family's MarkerWriter (which write its provider
// configurations build a client for), and PostCreateNeeded (whether a
// create withholds the marker at all), asked over every Terraform type the
// embedded mapping names plus synthetic ones, with the run's real facts.
func TestPostCreate_theThreeAnswersAgree(t *testing.T) {
	types := postCreateWalkTypes(t)
	facts := embeddedFacts(t)
	if bad := postCreateDisagreements(t, substrate.All, types, facts); len(bad) != 0 {
		t.Errorf("the post-create answers disagree:\n%s", strings.Join(bad, "\n"))
	}
	// The walk itself: a family that withholds and names no write is seen.
	withFamilyFirst(t, silentFamily{substrate.AWS})
	if bad := postCreateDisagreements(t, substrate.All, append(types, "silent_thing"), facts); len(bad) == 0 {
		t.Error("the walk did not see silentFamily withhold with no write")
	}
}

// postCreateDisagreements is the walk, asked of the families' raw answers
// rather than of any function in the code under test that combines them.
func postCreateDisagreements(t *testing.T, families []substrate.Substrate, types []string, facts substrate.Facts) []string {
	t.Helper()
	var bad []string
	for _, f := range families {
		writer := f.MarkerWriter()
		anyWrite := false
		for _, surface := range f.Surfaces() {
			post := f.Writes(surface).PostCreate
			switch {
			case post == "":
				bad = append(bad, fmt.Sprintf("%s/%s: Writes.PostCreate is unanswered", f.Name(), surface))
			case post != substrate.WriteNeverNeeded:
				anyWrite = true
				if writer != post {
					bad = append(bad, fmt.Sprintf("%s/%s: Writes.PostCreate is %q and MarkerWriter is %q", f.Name(), surface, post, writer))
				}
			}
			for _, typ := range types {
				created := substrate.Created{Addr: locatedTestAddr(t, typ, "x")}
				if _, needed := f.PostCreateNeeded(surface, created, facts); needed && (post == "" || post == substrate.WriteNeverNeeded) {
					bad = append(bad, fmt.Sprintf("%s/%s: %s withholds at create and the surface names post-create write %q", f.Name(), surface, typ, post))
				}
			}
		}
		if !anyWrite && writer != substrate.WriteNeverNeeded {
			bad = append(bad, fmt.Sprintf("%s: no surface names a post-create write and MarkerWriter is %q", f.Name(), writer))
		}
	}
	return bad
}

// postCreateWalkTypes is every tf_type live/mapping.json's embedded copy
// names, plus synthetic types no family's facts know.
func postCreateWalkTypes(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "registry", "mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Rows []struct {
			TFType string `json:"tf_type"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range m.Rows {
		seen[r.TFType] = true
	}
	if len(seen) < 1000 {
		t.Fatalf("the embedded mapping names %d types; the walk would see almost nothing", len(seen))
	}
	out := []string{"kubernetes_config_map", "kubernetes_manifest", "unknown_thing"}
	for typ := range seen {
		out = append(out, typ)
	}
	sort.Strings(out)
	return out
}

// embeddedFacts is the facts internal/command's markerFacts builds.
func embeddedFacts(t *testing.T) substrate.Facts {
	t.Helper()
	roster, err := registry.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	return substrate.Facts{substrate.AWS.Name(): roster}
}

// TestPostCreate_theWalkSeesAWSWithholding: the walk is not vacuous. The
// embedded roster has types AWS withholds for, and every one of them names
// the Tagging API write.
func TestPostCreate_theWalkSeesAWSWithholding(t *testing.T) {
	facts := embeddedFacts(t)
	var withheld []string
	for _, typ := range postCreateWalkTypes(t) {
		if _, needed := substrate.AWS.PostCreateNeeded(markers.SurfaceTags, substrate.Created{Addr: locatedTestAddr(t, typ, "x")}, facts); needed {
			withheld = append(withheld, typ)
		}
	}
	if len(withheld) == 0 {
		t.Fatal("no type in the embedded roster withholds at create: the walk above proves nothing about AWS")
	}
	if w := substrate.AWS.Writes(markers.SurfaceTags).PostCreate; w != substrate.WriteTaggingAPI {
		t.Errorf("AWS's tags surface names %q", w)
	}
	t.Logf("%d types withhold at create: %v", len(withheld), withheld)
}

// withheldMarkers is [NodeResolver.markersWithheld] on the tags surface,
// the shape the #1084 pins in nodetagoncreate_test.go call.
func (n *NodeResolver) withheldMarkers(addr addrs.AbsResourceInstance) map[string]string {
	return n.markersWithheld(markers.SurfaceTags, addr)
}

// withWrittenMarkers is [withMarkersAt] on the tags surface's carriers,
// the shape the #1316 pin in nodetagoncreate_test.go calls.
func withWrittenMarkers(obj cty.Value, written map[string]string) cty.Value {
	return withMarkersAt(obj, written, substrate.CarrierPaths(markers.SurfaceTags))
}

// TestWithMarkersAt_aMarkOnTheWayToTheCarrierIsNeverRewritten: a mark on
// a value enclosing a nested carrier (meta) leaves that carrier as the
// provider returned it, and carries the mark through, while an unmarked
// sibling carrier still takes the written markers.
func TestWithMarkersAt_aMarkOnTheWayToTheCarrierIsNeverRewritten(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{
		"id":     cty.StringVal("B1"),
		"labels": cty.NullVal(cty.Map(cty.String)),
		"tags":   cty.NullVal(cty.Map(cty.String)),
		"meta": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"labels": cty.NullVal(cty.Map(cty.String)),
		})}).Mark("sensitive"),
	})
	got := withMarkersAt(obj, map[string]string{markers.TagEstate: "prod"}, bareFamily{}.CarrierPaths(bareSurface))
	if mapEntry(got.GetAttr("labels"), markers.TagEstate) != "prod" {
		t.Errorf("the unmarked carrier did not take the marker: %#v", got.GetAttr("labels"))
	}
	meta := got.GetAttr("meta")
	if !meta.HasMark("sensitive") {
		t.Fatal("the mark on meta was lost")
	}
	if inner, _ := meta.Unmark(); !inner.Index(cty.NumberIntVal(0)).GetAttr("labels").IsNull() {
		t.Errorf("a carrier under a marked value was rewritten: %#v", inner)
	}
}
