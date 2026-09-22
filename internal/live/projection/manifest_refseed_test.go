// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1262. A kubernetes_manifest whose `manifest` reads another
// resource never bound to its own object: the configured seed is built by an
// evaluator that refuses a managed-resource reference, the refusal costs the
// WHOLE argument, and with no manifest in the prior there is no
// manifest.metadata.labels for the estate's marker to be read from. The
// instance is refused as UNOWNED and the plan proposes creating an object
// the estate itself applied.
//
// Proving these red: make [partialManifestSeed] return false and
// TestManifestReadingAnotherResourceBindsToItsOwnObject fails with the
// instance omitted as UNOWNED, which is the issue's own replan.

const refSeedEstate = "refseed-unit"

// refSeedCluster is a fake API server holding whole objects, keyed by the
// provider's import id, behind a fake provider that answers the way
// hashicorp/kubernetes's kubernetes_manifest does as far as this package can
// tell the difference: the import returns the live `object` and a null
// `manifest`, and the read refreshes `object` and hands `manifest` back
// exactly as the prior state held it, because nothing on a cluster answers
// for the DESIRED object.
type refSeedCluster struct {
	mu      sync.Mutex
	objects map[string]cty.Value

	// imported lists every import id asked for, in order.
	imported []string
	// unknownPrior names every import id whose ReadResource saw a prior
	// state that was not wholly known. A prior is a state; the protocol has
	// no unknowns in one.
	unknownPrior []string
}

func refSeedImportID(apiVersion, kind, namespace, name string) string {
	id := "apiVersion=" + apiVersion + ",kind=" + kind
	if namespace != "" {
		id += ",namespace=" + namespace
	}
	return id + ",name=" + name
}

func (c *refSeedCluster) put(obj cty.Value) string {
	meta := obj.GetAttr("metadata")
	ns := ""
	if meta.Type().HasAttribute("namespace") && !meta.GetAttr("namespace").IsNull() {
		ns = meta.GetAttr("namespace").AsString()
	}
	id := refSeedImportID(obj.GetAttr("apiVersion").AsString(), obj.GetAttr("kind").AsString(), ns, meta.GetAttr("name").AsString())
	if c.objects == nil {
		c.objects = map[string]cty.Value{}
	}
	c.objects[id] = obj
	return id
}

func (c *refSeedCluster) provider() (addrs.AbsProviderConfig, *tofu.MockProvider) {
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"kubernetes_manifest": manifestSeedSchema()},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.imported = append(c.imported, r.Target.ID)
		obj, ok := c.objects[r.Target.ID]
		if !ok {
			return providers.ImportResourceStateResponse{}
		}
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"manifest":        cty.NullVal(cty.DynamicPseudoType),
				"object":          obj,
				"computed_fields": cty.NullVal(cty.List(cty.String)),
			}),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		c.mu.Lock()
		defer c.mu.Unlock()
		key, _ := markers.ManifestKeyOf(cty.ObjectVal(map[string]cty.Value{"object": r.PriorState.GetAttr("object")}))
		id := refSeedImportID(key.APIVersion, key.Kind, key.Namespace, key.Name)
		if !r.PriorState.IsWhollyKnown() {
			c.unknownPrior = append(c.unknownPrior, id)
		}
		obj, ok := c.objects[id]
		if !ok {
			return providers.ReadResourceResponse{NewState: cty.NullVal(r.PriorState.Type())}
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"manifest":        r.PriorState.GetAttr("manifest"),
			"object":          obj,
			"computed_fields": r.PriorState.GetAttr("computed_fields"),
		})}
	}
	return provAddr, p
}

// refSeedLiveConfigMap is a ConfigMap as the provider reads one back: the
// metadata maps typed as maps of strings, the way the kind's OpenAPI schema
// types them, not as the object constructors a configuration writes.
func refSeedLiveConfigMap(name string, labels, annotations, data map[string]string) cty.Value {
	strMap := func(m map[string]string) cty.Value {
		if len(m) == 0 {
			return cty.NullVal(cty.Map(cty.String))
		}
		out := map[string]cty.Value{}
		for k, v := range m {
			out[k] = cty.StringVal(v)
		}
		return cty.MapVal(out)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("v1"),
		"kind":       cty.StringVal("ConfigMap"),
		"metadata": cty.ObjectVal(map[string]cty.Value{
			"name":        cty.StringVal(name),
			"namespace":   cty.StringVal("x"),
			"labels":      strMap(labels),
			"annotations": strMap(annotations),
		}),
		"data": strMap(data),
	})
}

func refSeedConfig(t *testing.T, body string) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return loadConfig(t, dir)
}

// refSeedPair is the issue's own reproduction, plus one leaf OUTSIDE the two
// metadata maps the #1177 mirror already answers for, so the fix cannot be
// passing on the mirror alone.
const refSeedPair = `
resource "kubernetes_manifest" "a" {
  manifest = {
    "apiVersion" = "v1"
    "kind"       = "ConfigMap"
    "metadata"   = { "name" = "a", "namespace" = "x" }
  }
}

resource "kubernetes_manifest" "b" {
  manifest = {
    "apiVersion" = "v1"
    "kind"       = "ConfigMap"
    "metadata" = {
      "annotations" = { "after" = kubernetes_manifest.a.manifest.metadata.name }
      "name"        = "b"
      "namespace"   = "x"
    }
    "data" = {
      "peer"  = kubernetes_manifest.a.manifest.metadata.name
      "fixed" = "literal"
    }
  }
}
`

// refSeedPriorManifest decodes one instance's projected prior and returns
// its `manifest` as plain JSON, which is what a by-value assertion wants.
func refSeedPriorManifest(t *testing.T, res *Result, addr addrs.AbsResourceInstance) string {
	t.Helper()
	is := res.State.ResourceInstance(addr)
	if is == nil || is.Current == nil {
		t.Fatalf("no object in the prior state for %s", addr)
	}
	obj, err := is.Current.Decode(manifestSeedSchema().Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding %s: %s\n%s", addr, err, is.Current.AttrsJSON)
	}
	manifest := obj.Value.GetAttr("manifest")
	if manifest.IsNull() {
		t.Fatalf("%s's prior carries no manifest at all:\n%s", addr, is.Current.AttrsJSON)
	}
	inManifest, okM := markers.ManifestKeyOf(cty.ObjectVal(map[string]cty.Value{"manifest": manifest}))
	onCluster, okO := markers.ManifestKeyOf(cty.ObjectVal(map[string]cty.Value{"object": obj.Value.GetAttr("object")}))
	if !okM || !okO || inManifest != onCluster {
		t.Errorf("%s's prior manifest names %+v and its live object names %+v; one instance, two objects", addr, inManifest, onCluster)
	}
	out, err := ctyjson.Marshal(manifest, manifest.Type())
	if err != nil {
		t.Fatalf("rendering %s's manifest: %s", addr, err)
	}
	var norm any
	if err := json.Unmarshal(out, &norm); err != nil {
		t.Fatal(err)
	}
	out, _ = json.Marshal(norm)
	return string(out)
}

func TestManifestReadingAnotherResourceBindsToItsOwnObject(t *testing.T) {
	cfg := refSeedConfig(t, refSeedPair)
	addrA := mustAddr(t, `kubernetes_manifest.a`)
	addrB := mustAddr(t, `kubernetes_manifest.b`)

	// The cluster one apply later: both objects exist and both carry the
	// estate's label, exactly as the issue's kubectl read shows them.
	cluster := &refSeedCluster{}
	own := map[string]string{markers.TagEstate: refSeedEstate}
	idA := cluster.put(refSeedLiveConfigMap("a", own, nil, nil))
	idB := cluster.put(refSeedLiveConfigMap("b", own,
		map[string]string{"after": "a", "kubectl.kubernetes.io/last-applied-configuration": "{}"},
		map[string]string{"peer": "a", "fixed": "literal"}))
	if want := "apiVersion=v1,kind=ConfigMap,namespace=x,name=b"; idB != want {
		t.Fatalf("fixture import id = %q, want %q", idB, want)
	}

	provAddr, p := cluster.provider()
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: addrA, Class: identity.ClassConcrete, ImportID: idA},
		{Addr: addrB, Class: identity.ClassConcrete, ImportID: idB},
	}, SingleProvider(provAddr, p), Options{Ownership: &Ownership{Estate: refSeedEstate}})
	assertNoErrors(t, diags)

	if !res.Has(addrA) {
		t.Fatalf("premise: the manifest with no reference did not bind; this test proves nothing about the one with a reference.\n%s", renderDiags(diags))
	}
	if !res.Has(addrB) {
		om, _ := res.OmissionFor(addrB)
		t.Fatalf("kubernetes_manifest.b is not in the prior state, so the plan proposes creating an object that exists (#1262). Omitted as %s: %s\n%s",
			om.Reason, om.Detail, renderDiags(diags))
	}
	if len(res.Unowned) != 0 {
		t.Errorf("an object carrying this estate's label was refused as unowned: %+v", res.Unowned)
	}
	if hasDiag(diags, "Live resource outside this estate", "name=b") {
		t.Errorf("the run still warns that b is outside the estate:\n%s", renderDiags(diags))
	}
	if len(cluster.unknownPrior) != 0 {
		t.Errorf("ReadResource was handed a prior state with unknown values in it for %v", cluster.unknownPrior)
	}

	// Bound by value, to the object the RESOLVER named and no other.
	asked := map[string]bool{}
	for _, id := range cluster.imported {
		asked[id] = true
	}
	if !asked[idB] || len(asked) != 2 {
		t.Errorf("imports asked for %v, want exactly %q and %q", cluster.imported, idA, idB)
	}

	// The prior manifest is what a state file would hold: the manifest as it
	// was last applied, the reference resolved, the estate's label on it. It
	// is also exactly what the plan-time evaluator makes of this block, which
	// is what lets the provider plan nothing.
	want := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"annotations": map[string]any{"after": "a"},
			"labels":      map[string]any{markers.TagEstate: refSeedEstate},
			"name":        "b",
			"namespace":   "x",
		},
		"data": map[string]any{"peer": "a", "fixed": "literal"},
	}
	wantJSON, _ := json.Marshal(want)
	if got := refSeedPriorManifest(t, res, addrB); got != string(wantJSON) {
		t.Errorf("kubernetes_manifest.b's prior manifest:\n got %s\nwant %s", got, wantJSON)
	}
}

// refSeedOpenPaths renders open paths the way an operator would write them,
// sorted, so a test can compare them by value.
func refSeedOpenPaths(open []cty.Path) []string {
	out := make([]string, 0, len(open))
	for _, p := range open {
		s := ""
		for _, step := range p {
			switch st := step.(type) {
			case cty.GetAttrStep:
				s += "." + st.Name
			case cty.IndexStep:
				if st.Key.Type() == cty.String {
					s += "[" + st.Key.AsString() + "]"
				} else {
					s += "[" + st.Key.AsBigFloat().String() + "]"
				}
			}
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TestPartialManifestSeedKeepsTheSkeleton pins what the seed IS when part of
// the manifest cannot be resolved: everything configuration states, with each
// unresolvable leaf a null whose path is remembered. The control arm is the
// strict seed, which must drop the manifest whole - that absence is #1262.
func TestPartialManifestSeedKeepsTheSkeleton(t *testing.T) {
	ctx := context.Background()
	cfg := refSeedConfig(t, refSeedPair)
	schema := manifestSeedSchema()
	rc := cfg.Module.ManagedResources["kubernetes_manifest.b"]

	// Named strictSeed, not strict: the package of that name is what the
	// seed's own secrets argument comes from, and a local shadowing it
	// would make the second call below unresolvable.
	strictSeed, _ := configuredAttrsSeed(ctx, cfg.Module.StaticEvaluator, cfg.Path, rc, schema, nil, strict.DefaultSecrets)
	if _, ok := strictSeed["manifest"]; ok {
		t.Fatal("the strict seed resolved a reference to another resource; this control must fail, or the test below proves nothing")
	}

	seed, open, ok := partialManifestSeed(ctx, cfg.Module.StaticEvaluator, cfg.Path, rc, schema)
	if !ok {
		t.Fatal("no partial seed for a manifest whose identity and structure are all literal")
	}
	if !seed.IsWhollyKnown() {
		t.Errorf("the seed carries an unknown, which no prior state may: %#v", seed)
	}
	if got, want := refSeedOpenPaths(open), []string{".data.peer", ".metadata.annotations.after"}; !reflect.DeepEqual(got, want) {
		t.Errorf("open paths = %v, want %v", got, want)
	}
	for _, p := range open {
		if v, err := p.Apply(seed); err != nil || !v.IsNull() {
			t.Errorf("open path %v is not a null in the seed: %#v, %v", p, v, err)
		}
	}
	if got := seed.GetAttr("data").GetAttr("fixed"); got.IsNull() || got.AsString() != "literal" {
		t.Errorf("a literal beside an open leaf was lost: %#v", got)
	}
	key, ok := markers.ManifestKeyOf(cty.ObjectVal(map[string]cty.Value{"manifest": seed}))
	if !ok || key != (markers.ManifestKey{APIVersion: "v1", Kind: "ConfigMap", Namespace: "x", Name: "b"}) {
		t.Errorf("the seed names %+v, want the configuration's own object", key)
	}

	// A manifest with nothing unresolvable is not this function's business:
	// the strict seed answers it and prepareRead never asks.
	rcA := cfg.Module.ManagedResources["kubernetes_manifest.a"]
	if strictA, _ := configuredAttrsSeed(ctx, cfg.Module.StaticEvaluator, cfg.Path, rcA, schema, nil, strict.DefaultSecrets); strictA["manifest"] == cty.NilVal {
		t.Error("premise: the strict seed no longer answers a wholly literal manifest")
	}
}

// TestPartialManifestSeedDeclinesAnUnresolvableIdentity is the safety half.
// apiVersion, kind, metadata.name and metadata.namespace say which object
// this is; when configuration alone cannot state any one of them there is no
// seed, because the read side fills open leaves from whatever object came
// back and an identity filled that way agrees with it by construction.
func TestPartialManifestSeedDeclinesAnUnresolvableIdentity(t *testing.T) {
	ctx := context.Background()
	schema := manifestSeedSchema()
	ref := `kubernetes_manifest.a.manifest.metadata.name`
	for _, tc := range []struct{ name, apiVersion, kind, meta string }{
		{"name", `"v1"`, `"ConfigMap"`, `{ "name" = ` + ref + `, "namespace" = "x" }`},
		{"templated name", `"v1"`, `"ConfigMap"`, `{ "name" = "b-${` + ref + `}", "namespace" = "x" }`},
		{"namespace", `"v1"`, `"ConfigMap"`, `{ "name" = "b", "namespace" = ` + ref + ` }`},
		{"kind", `"v1"`, ref, `{ "name" = "b", "namespace" = "x" }`},
		{"apiVersion", ref, `"ConfigMap"`, `{ "name" = "b", "namespace" = "x" }`},
		{"metadata", `"v1"`, `"ConfigMap"`, `kubernetes_manifest.a.manifest.metadata`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := refSeedConfig(t, `
resource "kubernetes_manifest" "a" {
  manifest = { "apiVersion" = "v1", "kind" = "ConfigMap", "metadata" = { "name" = "a", "namespace" = "x" } }
}
resource "kubernetes_manifest" "b" {
  manifest = {
    "apiVersion" = `+tc.apiVersion+`
    "kind"       = `+tc.kind+`
    "metadata"   = `+tc.meta+`
  }
}
`)
			rc := cfg.Module.ManagedResources["kubernetes_manifest.b"]
			if seed, open, ok := partialManifestSeed(ctx, cfg.Module.StaticEvaluator, cfg.Path, rc, schema); ok {
				t.Errorf("seeded %#v with open paths %v; an identity configuration cannot state must not be seeded", seed, refSeedOpenPaths(open))
			}
		})
	}
}

// TestManifestWithAnUnresolvableNameIsNotBound says what happens in that
// case end to end, and it is what happened before #1262's fix: the instance
// is left out of the prior with the UNOWNED warning that names it. Even when
// a resolution names an object that exists and carries this estate's label,
// the projection does not complete the manifest's identity from the object
// it read, so it cannot bind to a guess.
func TestManifestWithAnUnresolvableNameIsNotBound(t *testing.T) {
	cfg := refSeedConfig(t, `
resource "kubernetes_manifest" "a" {
  manifest = { "apiVersion" = "v1", "kind" = "ConfigMap", "metadata" = { "name" = "a", "namespace" = "x" } }
}
resource "kubernetes_manifest" "b" {
  manifest = {
    "apiVersion" = "v1"
    "kind"       = "ConfigMap"
    "metadata"   = { "name" = "b-${kubernetes_manifest.a.manifest.metadata.name}", "namespace" = "x" }
  }
}
`)
	addrB := mustAddr(t, `kubernetes_manifest.b`)
	cluster := &refSeedCluster{}
	id := cluster.put(refSeedLiveConfigMap("b-a", map[string]string{markers.TagEstate: refSeedEstate}, nil, nil))

	provAddr, p := cluster.provider()
	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		{Addr: addrB, Class: identity.ClassConcrete, ImportID: id},
	}, SingleProvider(provAddr, p), Options{Ownership: &Ownership{Estate: refSeedEstate}})
	assertNoErrors(t, diags)

	if res.Has(addrB) {
		t.Fatal("an instance whose manifest does not state its own name entered the prior state")
	}
	if om := omissionFor(t, res, `kubernetes_manifest.b`); om.Reason != ReasonUnowned {
		t.Errorf("omitted as %s, want %s", om.Reason, ReasonUnowned)
	}
	if !hasDiag(diags, "Live resource outside this estate", id) {
		t.Errorf("the omission is silent; it must name the object:\n%s", renderDiags(diags))
	}
}

// TestPartialManifestSeedStillRefusesRepetition: the tolerant evaluator
// answers a resource reference with an unknown and nothing else. An expanded
// block's own count.index is answered by seedRepetition from the instance
// key or not at all.
func TestPartialManifestSeedStillRefusesRepetition(t *testing.T) {
	cfg := refSeedConfig(t, `
resource "kubernetes_manifest" "shard" {
  count = 2
  manifest = {
    "apiVersion" = "v1"
    "kind"       = "ConfigMap"
    "metadata"   = { "name" = "fixed", "namespace" = "x", "annotations" = { "i" = "n-${count.index}" } }
  }
}
`)
	rc := cfg.Module.ManagedResources["kubernetes_manifest.shard"]
	if _, open, ok := partialManifestSeed(context.Background(), cfg.Module.StaticEvaluator, cfg.Path, rc, manifestSeedSchema()); ok {
		t.Errorf("count.index with no repetition data was treated as an open leaf: %v", refSeedOpenPaths(open))
	}
}

// TestFillManifestOpenPaths pins the read side leaf by leaf.
func TestFillManifestOpenPaths(t *testing.T) {
	block := manifestSeedSchema().Block
	attr := func(names ...string) cty.Path {
		var p cty.Path
		for _, n := range names {
			p = p.GetAttr(n)
		}
		return p
	}
	manifest := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("v1"),
		"kind":       cty.StringVal("ConfigMap"),
		"metadata":   cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("b"), "namespace": cty.StringVal("x")}),
		"data": cty.ObjectVal(map[string]cty.Value{
			"peer":     cty.NullVal(cty.DynamicPseudoType), // open, the live object answers
			"templ":    cty.NullVal(cty.String),            // open and typed, the live object answers
			"missing":  cty.NullVal(cty.DynamicPseudoType), // open, the live object has no such key
			"declared": cty.NullVal(cty.String),            // a null the CONFIGURATION wrote, never open
		}),
		"spec": cty.ObjectVal(map[string]cty.Value{
			"replicas": cty.NullVal(cty.String),            // open and typed string, the live object holds a number
			"selector": cty.NullVal(cty.DynamicPseudoType), // open, the live object holds a whole object
			"args":     cty.TupleVal([]cty.Value{cty.NullVal(cty.DynamicPseudoType), cty.StringVal("lit")}),
		}),
	})
	live := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("v1"),
		"kind":       cty.StringVal("ConfigMap"),
		"metadata":   cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal("b"), "namespace": cty.StringVal("x")}),
		"data": cty.MapVal(map[string]cty.Value{
			"peer": cty.StringVal("drifted"), "templ": cty.StringVal("a-x"), "declared": cty.StringVal("server-side"),
		}),
		"spec": cty.ObjectVal(map[string]cty.Value{
			"replicas": cty.NumberIntVal(3),
			"selector": cty.ObjectVal(map[string]cty.Value{"app": cty.StringVal("x")}),
			"args":     cty.ListVal([]cty.Value{cty.StringVal("first"), cty.StringVal("lit")}),
		}),
	})
	v := cty.ObjectVal(map[string]cty.Value{
		"manifest": manifest, "object": live, "computed_fields": cty.NullVal(cty.List(cty.String)),
	})
	open := []cty.Path{
		attr("data", "peer"), attr("data", "templ"), attr("data", "missing"),
		attr("spec", "replicas"), attr("spec", "selector"),
		attr("spec", "args").IndexInt(0),
	}

	got := fillManifestOpenPaths(v, block, open).GetAttr("manifest")
	data, spec := got.GetAttr("data"), got.GetAttr("spec")
	for name, want := range map[string]cty.Value{
		// The live value, even where it is not what the configuration
		// would now say: the prior then differs from the configuration and
		// the provider plans the difference.
		"peer":  cty.StringVal("drifted"),
		"templ": cty.StringVal("a-x"),
		// No witness: the leaf stays null.
		"missing": cty.NullVal(cty.DynamicPseudoType),
		// Never open, so never filled, whatever the server holds there.
		"declared": cty.NullVal(cty.String),
	} {
		if g := data.GetAttr(name); !g.RawEquals(want) {
			t.Errorf("data.%s = %#v, want %#v", name, g, want)
		}
	}
	if g := spec.GetAttr("replicas"); !g.RawEquals(cty.StringVal("3")) {
		t.Errorf("spec.replicas = %#v, want the live number converted to the leaf's own type", g)
	}
	if g := spec.GetAttr("selector"); !g.IsNull() {
		t.Errorf("spec.selector = %#v; a whole live sub-object carries the server's defaults and is not a last-applied value", g)
	}
	if g := spec.GetAttr("args").Index(cty.NumberIntVal(0)); !g.RawEquals(cty.StringVal("first")) {
		t.Errorf("spec.args[0] = %#v, want the live list's element", g)
	}

	// Nothing open, or not a manifest-surface object: untouched.
	if out := fillManifestOpenPaths(v, block, nil); !out.RawEquals(v) {
		t.Error("a read with nothing open was rewritten")
	}
	if out := fillManifestOpenPaths(v, stubLaunchConfigSchema().Block, open); !out.RawEquals(v) {
		t.Error("a type that is not manifest-shaped was rewritten")
	}
}

// TestPartialSeedDoesNotVouchForSomebodyElsesObject: the partial seed is
// stamped with this estate's label like any other manifest seed, and the
// mirror then replaces that label with what the LIVE object carries. An
// object at b's identity that another estate labelled, or nobody did, is
// still refused - the seed widened what is read, never what is owned.
func TestPartialSeedDoesNotVouchForSomebodyElsesObject(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
	}{
		{"unlabelled", nil},
		{"another estate", map[string]string{markers.TagEstate: "somebody-else"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := refSeedConfig(t, refSeedPair)
			addrB := mustAddr(t, `kubernetes_manifest.b`)
			cluster := &refSeedCluster{}
			id := cluster.put(refSeedLiveConfigMap("b", tc.labels, map[string]string{"after": "a"}, map[string]string{"peer": "a"}))

			provAddr, p := cluster.provider()
			res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
				{Addr: addrB, Class: identity.ClassConcrete, ImportID: id},
			}, SingleProvider(provAddr, p), Options{Ownership: &Ownership{Estate: refSeedEstate}})
			assertNoErrors(t, diags)
			if res.Has(addrB) {
				t.Fatalf("an object this estate does not label entered the prior state:\n%s", renderDiags(diags))
			}
			if len(res.Unowned) != 1 || res.Unowned[0].ImportID != id {
				t.Errorf("the refusal is not on the result: %+v", res.Unowned)
			}
		})
	}
}
