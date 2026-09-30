// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1640 (#1605 step 2): the sweep reads the address annotation
// #1639 stamps, and an object whose annotation names a declared instance
// is that instance's object, bound at its address, where before it was
// filed as orphan_<namespace>_<name>.

func TestAddressAnnotationSpelledTheSameInBothPackages(t *testing.T) {
	if kubesweep.AddressAnnotation != markers.AddressAnnotation {
		t.Fatalf("kubesweep.AddressAnnotation = %q, markers.AddressAnnotation = %q; the sweep would read an annotation nothing writes", kubesweep.AddressAnnotation, markers.AddressAnnotation)
	}
}

func configMapKind() kubesweep.Kind {
	return kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map", "kubernetes_config_map_v1"}}
}

func cronTabKind() kubesweep.Kind {
	return kubesweep.Kind{GVR: schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}, Kind: "CronTab", Namespaced: true, APIVersion: "stable.example.com/v1", TypeNames: []string{"kubernetes_manifest"}, Manifest: true}
}

func labelled(kind, ns, name, address string) kubesweep.Object {
	return kubesweep.Object{Kind: kind, Namespace: ns, Name: name, ImportID: ns + "/" + name, Labels: map[string]string{"tofu-estate": "m1116"}, Address: address}
}

func k8sTypes() []string {
	return []string{"kubernetes_config_map", "kubernetes_config_map_v1", "kubernetes_manifest"}
}

// issue1539Request is #1539's reproduction after its first apply: the
// CronTab a manifest block declares, and the ConfigMap whose name reads
// the CronTab's spec.image - a non-identity attribute, so the static
// evaluator refused it and the node took it over. The ConfigMap the first
// apply created carries the annotation naming its block.
func issue1539Request(sweeper *stubSweeper) Request {
	reader := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_config_map_v1", Name: "reader"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	crontab := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_manifest", Name: "crontab"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	return Request{
		Estate:   "m1116",
		Sweepers: []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
		Resolutions: []identity.Resolution{
			{Addr: crontab, Class: identity.ClassConcrete,
				ImportID: kubesweep.ManifestImportID("stable.example.com/v1", "CronTab", "m1116-res", "my-crontab")},
		},
		NodeRefused: map[string]bool{reader.String(): true},
	}
}

func issue1539Sweeper(readerAnnotation string) *stubSweeper {
	return &stubSweeper{
		kinds: []kubesweep.Kind{configMapKind(), cronTabKind()},
		objects: map[string][]kubesweep.Object{
			"ConfigMap": {labelled("ConfigMap", "m1116-res", "my-awesome-cron-image-reader", readerAnnotation)},
			"CronTab": {{Kind: "CronTab", Namespace: "m1116-res", Name: "my-crontab", Labels: map[string]string{"tofu-estate": "m1116"}, Address: "kubernetes_manifest.crontab",
				ImportID: kubesweep.ManifestImportID("stable.example.com/v1", "CronTab", "m1116-res", "my-crontab")}},
		},
	}
}

func resolutionAt(res *Result, addr string) (identity.Resolution, bool) {
	for _, r := range res.Resolutions {
		if r.Addr.String() == addr {
			return r, true
		}
	}
	return identity.Resolution{}, false
}

// configMapSchema is kubernetes_config_map_v1 as the node sees it: a
// label-surface type, whose marker carries no address (#1617's refusal
// applies to it until #1641 flips CarriesAddress).
func configMapSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"data": {Type: cty.Map(cty.String), Optional: true},
			"id":   {Type: cty.String, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1, Block: configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"annotations": {Type: cty.Map(cty.String), Optional: true},
					"labels":      {Type: cty.Map(cty.String), Optional: true},
					"name":        {Type: cty.String, Optional: true, Computed: true},
					"namespace":   {Type: cty.String, Optional: true},
				},
			}},
		},
	}}
}

func readerValue() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"data": cty.NullVal(cty.Map(cty.String)),
		"id":   cty.NullVal(cty.String),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"annotations": cty.NullVal(cty.Map(cty.String)),
			"labels":      cty.NullVal(cty.Map(cty.String)),
			"name":        cty.StringVal("my-awesome-cron-image-reader"),
			"namespace":   cty.StringVal("m1116-res"),
		})}),
	})
}

// TestKubernetesSweepBindsByAddressAnnotation is #1539's reproduction at
// the plan's second run, end to end through the three pieces that decide
// it: the sweep, the marker index the command layer builds from the
// sweep's resolutions, and the node resolver holding the static refusal.
// Before #1640 the sweep filed the ConfigMap as
// kubernetes_config_map_v1.orphan_m1116-res_my-awesome-cron-image-reader
// and the node, finding nothing, raised #1617's refusal (or, before
// #1617, planned a create over it). Now the object is bound at
// kubernetes_config_map_v1.reader, the node answers from the index, and
// nothing is proposed for removal: the second plan has nothing to do.
func TestKubernetesSweepBindsByAddressAnnotation(t *testing.T) {
	reader := k8sInstance(t, "kubernetes_config_map_v1", "reader")
	res := &Result{}
	req := issue1539Request(issue1539Sweeper(markers.EscapeAddress(reader.String())))
	res.Resolutions = append(res.Resolutions, req.Resolutions...)
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	if len(res.Orphans) != 0 {
		t.Fatalf("orphans = %v; the ConfigMap whose annotation names kubernetes_config_map_v1.reader is that block's object", res.Orphans)
	}
	r, ok := resolutionAt(res, reader.String())
	if !ok || r.Class != identity.ClassConcrete || r.ImportID != "m1116-res/my-awesome-cron-image-reader" {
		t.Fatalf("resolution at %s = %+v (present %v); want concrete m1116-res/my-awesome-cron-image-reader", reader, r, ok)
	}
	if r.IdentityValues["name"] != "my-awesome-cron-image-reader" || r.IdentityValues["namespace"] != "m1116-res" {
		t.Errorf("identity values = %v, want the object's name and namespace", r.IdentityValues)
	}
	b, ok := res.BindingFor(reader)
	if !ok || b.ImportID != "m1116-res/my-awesome-cron-image-reader" || b.TypeName != "kubernetes_config_map_v1" {
		t.Errorf("binding = %+v (present %v)", b, ok)
	}
	if !res.AddressBound[reader.String()] {
		t.Errorf("AddressBound = %v, want %s", res.AddressBound, reader)
	}
	if !res.MarkerVerified()[reader.String()] {
		t.Errorf("the binding is not marker-verified; the object carries this estate's label and was listed by it")
	}

	var refusal tfdiags.Diagnostics
	refusal = refusal.Append(tfdiags.Sourceless(tfdiags.Error, "Identity not resolvable from configuration", "reader.name refers to kubernetes_manifest.crontab.object.spec.image"))
	node := &projection.NodeResolver{
		MarkerIndex:    projection.NewMarkerIndex(res.Resolutions),
		StaticRefusals: map[string]tfdiags.Diagnostics{reader.String(): refusal},
	}
	target, found, diags := node.ResolveResourceIdentity(context.Background(), reader, readerValue(), configMapSchema())
	if diags.HasErrors() || !found || target.ID != "m1116-res/my-awesome-cron-image-reader" {
		t.Fatalf("node: target=%#v found=%v diags=%v; want the listed object bound, no refusal", target, found, diags.ErrWithWarnings())
	}
}

// Without the annotation - an object an older build made, or one a
// controller stripped - nothing changes: the object is the orphan it was,
// and the node's refusal (#1617) stands. #1641 is what widens this.
func TestKubernetesSweepWithoutAnnotationIsUnchanged(t *testing.T) {
	reader := k8sInstance(t, "kubernetes_config_map_v1", "reader")
	res := &Result{}
	req := issue1539Request(issue1539Sweeper(""))
	res.Resolutions = append(res.Resolutions, req.Resolutions...)
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if len(res.Orphans) != 1 || res.Orphans[0].ImportID != "m1116-res/my-awesome-cron-image-reader" {
		t.Fatalf("orphans = %v, want the unannotated ConfigMap", res.Orphans)
	}
	if _, ok := resolutionAt(res, reader.String()); ok {
		t.Errorf("an unannotated object was bound to %s", reader)
	}
}

// #1541's shape: a create_before_destroy block whose name is content
// hashed. The configuration now names cfg-b, which does not exist yet;
// cfg-a, the object the block made, carries the block's address. It is
// bound at the block's address (so the plan is a replace, as stock's is)
// rather than filed as an orphan beside a create.
func TestKubernetesSweepRebindsARenamedObjectByAddress(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map_v1", "cfg")
	sweeper := &stubSweeper{
		kinds: []kubesweep.Kind{configMapKind()},
		objects: map[string][]kubesweep.Object{
			"ConfigMap": {labelled("ConfigMap", "rep-chdf", "cfg-a", cfg.String())},
		},
	}
	req := Request{
		Estate:   "m1116",
		Sweepers: []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
		Resolutions: []identity.Resolution{
			{Addr: cfg, Class: identity.ClassConcrete, ImportID: "rep-chdf/cfg-b", IdentityValues: map[string]string{"name": "cfg-b", "namespace": "rep-chdf"}},
		},
	}
	res := &Result{}
	res.Resolutions = append(res.Resolutions, req.Resolutions...)
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if len(res.Orphans) != 0 {
		t.Fatalf("orphans = %v; cfg-a is %s's object", res.Orphans, cfg)
	}
	r, _ := resolutionAt(res, cfg.String())
	if r.ImportID != "rep-chdf/cfg-a" || r.IdentityValues["name"] != "cfg-a" {
		t.Fatalf("resolution = %+v, want it rebound to rep-chdf/cfg-a", r)
	}
	n := 0
	for _, r := range res.Resolutions {
		if r.Addr.String() == cfg.String() {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d resolutions at %s, want the one rewritten in place", n, cfg)
	}
}

// Every shape the annotation does NOT bind: each stays exactly the orphan
// it was before #1640. A wrong binding is worse than an orphan here - it
// would import one object at another block's address.
func TestKubernetesSweepAddressAnnotationBindsOnlyWhatItMust(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map_v1", "cfg")
	gone := k8sInstance(t, "kubernetes_config_map_v1", "gone")
	manifest := k8sInstance(t, "kubernetes_manifest", "cm")
	for _, tc := range []struct {
		name        string
		objects     []kubesweep.Object
		resolutions []identity.Resolution
		refused     map[string]bool
		scope       addrs.AbsProviderConfig
		wantOrphans []string
	}{
		{
			name:        "the annotation names an address nothing declares",
			objects:     []kubesweep.Object{labelled("ConfigMap", "ns", "left", gone.String())},
			wantOrphans: []string{"ns/left"},
		},
		{
			name: "the declared object is listed, so the address already has its object",
			objects: []kubesweep.Object{
				labelled("ConfigMap", "ns", "cfg-b", cfg.String()),
				labelled("ConfigMap", "ns", "cfg-a", cfg.String()),
			},
			resolutions: []identity.Resolution{{Addr: cfg, Class: identity.ClassConcrete, ImportID: "ns/cfg-b"}},
			wantOrphans: []string{"ns/cfg-a"},
		},
		// Two objects claiming one address were both orphans here until
		// #1641; they are the collision refusal now, in
		// TestKubernetesSweepTwoClaimantsAreACollision.
		{
			name:        "the annotation names a type that does not manage the kind",
			objects:     []kubesweep.Object{labelled("ConfigMap", "ns", "x", "kubernetes_secret_v1.s")},
			refused:     map[string]bool{"kubernetes_secret_v1.s": true},
			wantOrphans: []string{"ns/x"},
		},
		{
			name:        "the annotation does not parse as an address",
			objects:     []kubesweep.Object{labelled("ConfigMap", "ns", "x", "not an address")},
			wantOrphans: []string{"ns/x"},
		},
		{
			name: "the object is terminating",
			objects: []kubesweep.Object{func() kubesweep.Object {
				o := labelled("ConfigMap", "ns", "x", cfg.String())
				o.DeletionTimestamp = "2026-09-26T00:00:00Z"
				return o
			}()},
			refused:     map[string]bool{cfg.String(): true},
			wantOrphans: []string{"ns/x"},
		},
		{
			// Not bound here, and not an orphan either (GitHub issue
			// #1757): the pass whose configuration declares cfg decides
			// the object, and an orphan here was a destroy beside that
			// pass's import. Empty, not nil: nil asserts the manifest
			// binding below.
			name:        "the address is declared under another provider configuration",
			objects:     []kubesweep.Object{labelled("ConfigMap", "ns", "x", cfg.String())},
			refused:     map[string]bool{cfg.String(): true},
			scope:       addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes"), Alias: "other"},
			wantOrphans: []string{},
		},
		{
			// A manifest block may declare a built-in kind; its object
			// imports by the manifest id, not NAMESPACE/NAME. Bound, and
			// not an orphan.
			name:        "a manifest block's ConfigMap binds by the manifest id",
			objects:     []kubesweep.Object{labelled("ConfigMap", "ns", "m", manifest.String())},
			refused:     map[string]bool{manifest.String(): true},
			wantOrphans: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sweeper := &stubSweeper{kinds: []kubesweep.Kind{configMapKind()}, objects: map[string][]kubesweep.Object{"ConfigMap": tc.objects}}
			req := Request{
				Estate:        "m1116",
				Sweepers:      []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
				Resolutions:   tc.resolutions,
				NodeRefused:   tc.refused,
				ScopeProvider: tc.scope,
				Config:        k8sScopeConfig(t),
			}
			res := &Result{}
			res.Resolutions = append(res.Resolutions, req.Resolutions...)
			if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
				t.Fatalf("unexpected errors: %s", diags.Err())
			}
			var got []string
			for _, o := range res.Orphans {
				got = append(got, o.ImportID)
			}
			if len(got) != len(tc.wantOrphans) {
				t.Fatalf("orphans = %v, want %v", got, tc.wantOrphans)
			}
			for i := range got {
				if got[i] != tc.wantOrphans[i] {
					t.Fatalf("orphans = %v, want %v", got, tc.wantOrphans)
				}
			}
			for _, o := range res.Orphans {
				if o.AddressAnnotation == "" {
					t.Errorf("orphan %s lost its annotation", o.ImportID)
				}
			}
			if tc.wantOrphans == nil {
				r, ok := resolutionAt(res, manifest.String())
				if !ok || r.ImportID != kubesweep.ManifestImportID("v1", "ConfigMap", "ns", "m") {
					t.Errorf("manifest resolution = %+v (present %v)", r, ok)
				}
			} else if len(res.AddressBound) != 0 || len(res.Bindings) != 0 {
				t.Errorf("bound %v / %v; nothing should bind", res.AddressBound, res.Bindings)
			}
		})
	}
}

// k8sScopeConfig declares the blocks the table above names, each under
// the default kubernetes provider configuration.
func k8sScopeConfig(t *testing.T) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	src := `
resource "kubernetes_config_map_v1" "cfg" {
  metadata {
    name      = "cfg-${var.v}"
    namespace = "ns"
  }
}
variable "v" { default = "b" }
resource "kubernetes_secret_v1" "s" {
  metadata {
    name      = "s"
    namespace = "ns"
  }
}
resource "kubernetes_manifest" "cm" {
  manifest = { apiVersion = "v1", kind = "ConfigMap", metadata = { name = "m", namespace = "ns" } }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return loadConfig(t, dir)
}

// TestMergeKeepsTheAddressBoundResolution: on a run with an AWS provider
// beside the Kubernetes one, every pass carries the configuration's own
// concrete resolution for a Kubernetes block, and only the Kubernetes
// pass rebinds it (#1541's rename). Both are bound classes, so the merge
// must be told which one wins, whichever order the passes ran in.
func TestMergeKeepsTheAddressBoundResolution(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map_v1", "cfg")
	fromConfig := identity.Resolution{Addr: cfg, Class: identity.ClassConcrete, ImportID: "rep-chdf/cfg-b"}
	rebound := identity.Resolution{Addr: cfg, Class: identity.ClassConcrete, ImportID: "rep-chdf/cfg-a"}

	aws := &Result{}
	aws.Resolutions = []identity.Resolution{fromConfig}
	kube := &Result{}
	kube.Resolutions = []identity.Resolution{rebound}
	kube.AddressBound = map[string]bool{cfg.String(): true}
	kube.Bindings = []Binding{{Addr: cfg, TypeName: "kubernetes_config_map_v1", ImportID: "rep-chdf/cfg-a"}}

	awsPass := Pass{Provider: testProviderAddr(t, ""), Result: aws}
	kubePass := Pass{Provider: addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}, Result: kube}
	for name, passes := range map[string][]Pass{
		"aws first":  {awsPass, kubePass},
		"kube first": {kubePass, awsPass},
	} {
		t.Run(name, func(t *testing.T) {
			merged, _, diags := Merge(estateName, passes, false)
			assertNoErrors(t, diags)
			var got []string
			for _, r := range merged.Resolutions {
				if r.Addr.String() == cfg.String() {
					got = append(got, r.ImportID)
				}
			}
			if len(got) != 1 || got[0] != "rep-chdf/cfg-a" {
				t.Fatalf("merged resolutions at %s = %v, want [rep-chdf/cfg-a]", cfg, got)
			}
			if !merged.AddressBound[cfg.String()] {
				t.Errorf("AddressBound lost in the merge: %v", merged.AddressBound)
			}
		})
	}
}
