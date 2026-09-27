// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configload"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The Kubernetes half of live-ls (GitHub issue #1081, item 1): the
// listing is the sweep's listing - one label-selected list per kind the
// cluster serves - and what it calls declared is the sweep's join, the
// kind and the natural key. These tests drive liveLsKubernetesList through
// a stub Sweeper rather than a cluster; live/smoke/scenarios/
// k8s-greenfield.sh is the same listing against kind.

// liveLsStubSweeper stands in for a cluster: fixed kinds, fixed objects
// per kind, one kind whose list fails, or discovery failing outright.
type liveLsStubSweeper struct {
	kinds     []kubesweep.Kind
	objects   map[string][]kubesweep.Object
	failKind  string
	kindsErr  error
	selectors []string
	held      map[string][]kubesweep.HeldObject
}

func (s *liveLsStubSweeper) Kinds(_ context.Context, _ []string, _ string) ([]kubesweep.Kind, []string, error) {
	if s.kindsErr != nil {
		return nil, nil, s.kindsErr
	}
	return s.kinds, nil, nil
}

// Serves is never asked by live-ls, which lists what the cluster has and
// refuses nothing; it is here only because the sweep's interface carries
// it for the plan's missing-CRD refusal (#1079).
func (s *liveLsStubSweeper) Serves(_ context.Context, _, _ string) (bool, error) {
	return true, nil
}

// DryRun is never asked by live-ls either; it is the plan's post-plan
// evidence (#1081, item 3).
func (s *liveLsStubSweeper) DryRun(_ context.Context, _ map[string]any, _ bool) (kubesweep.DryRunResult, error) {
	return kubesweep.DryRunResult{Accepted: true}, nil
}

func (s *liveLsStubSweeper) List(_ context.Context, k kubesweep.Kind, key, value string) ([]kubesweep.Object, kubesweep.Skipped, error) {
	s.selectors = append(s.selectors, k.Kind+" "+key+"="+value)
	if k.Kind == s.failKind {
		return nil, kubesweep.Skipped{}, errors.New("forbidden")
	}
	return s.objects[k.Kind], kubesweep.Skipped{Count: len(s.held[k.Kind]), Held: s.held[k.Kind]}, nil
}

func liveLsK8sResolution(t *testing.T, typeName, name, importID string) identity.Resolution {
	t.Helper()
	return identity.Resolution{
		Addr:     liveLsTestResolution(t, typeName+"."+name, identity.ClassConcrete).Addr,
		Class:    identity.ClassConcrete,
		ImportID: importID,
	}
}

func TestLiveLsKubernetesList(t *testing.T) {
	cm := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map", "kubernetes_config_map_v1"}}
	ns := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}, Kind: "Namespace", APIVersion: "v1", TypeNames: []string{"kubernetes_namespace"}}
	sa := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}, Kind: "ServiceAccount", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_service_account"}}
	ct := kubesweep.Kind{GVR: schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}, Kind: "CronTab", Namespaced: true, APIVersion: "stable.example.com/v1", TypeNames: []string{"kubernetes_manifest"}, Manifest: true}
	labels := map[string]string{"tofu-estate": "smoke-k8s", "app": "web"}
	sweeper := &liveLsStubSweeper{
		kinds: []kubesweep.Kind{cm, ns, sa, ct},
		objects: map[string][]kubesweep.Object{
			"ConfigMap": {
				{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "app-config", ImportID: "smoke-k8s/app-config", Labels: labels},
				{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "stray", ImportID: "smoke-k8s/stray", Labels: map[string]string{"tofu-estate": "smoke-k8s"}},
			},
			"Namespace": {{Kind: "Namespace", Name: "smoke-k8s", ImportID: "smoke-k8s", Labels: map[string]string{"tofu-estate": "smoke-k8s"}}},
			"CronTab": {
				{Kind: "CronTab", Namespace: "smoke-k8s", Name: "my-crontab", ImportID: "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-k8s,name=my-crontab", Labels: labels},
				{Kind: "CronTab", Namespace: "smoke-k8s", Name: "stray", ImportID: "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-k8s,name=stray", Labels: labels},
			},
		},
		failKind: "ServiceAccount",
	}
	types := []string{"kubernetes_config_map", "kubernetes_config_map_v1", "kubernetes_manifest", "kubernetes_namespace", "kubernetes_service_account"}
	resolutions := []identity.Resolution{
		liveLsK8sResolution(t, "kubernetes_config_map_v1", "app", "smoke-k8s/app-config"),
		liveLsK8sResolution(t, "kubernetes_namespace", "app", "smoke-k8s"),
		liveLsK8sResolution(t, "kubernetes_manifest", "crontab", "apiVersion=stable.example.com/v1,kind=CronTab,namespace=smoke-k8s,name=my-crontab"),
	}

	items, diags := liveLsKubernetesList(context.Background(), "smoke-k8s", sweeper, types, "kubernetes_manifest", resolutions, nil)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	// Every listed kind was selected on the estate label, nothing else.
	for _, sel := range sweeper.selectors {
		if !strings.HasSuffix(sel, " tofu-estate=smoke-k8s") {
			t.Errorf("list selector %q is not the estate label", sel)
		}
	}
	if len(sweeper.selectors) != 4 {
		t.Errorf("lists issued = %v, want one per kind", sweeper.selectors)
	}

	got := map[string]string{}
	for _, it := range items {
		if it.Source != "kubernetes" {
			t.Errorf("%s %s: Source = %q, want kubernetes", it.Kind, it.ID, it.Source)
		}
		got[it.Kind+" "+it.ID] = it.Type + "|" + it.Address + "|" + boolWord(it.Declared) + "|" + it.APIVersion + "|" + it.Tags["app"]
	}
	want := map[string]string{
		// Declared through the versioned type: filed under the block's own
		// type, not the sweep's default for the kind.
		"ConfigMap smoke-k8s/app-config": "kubernetes_config_map_v1|kubernetes_config_map_v1.app|declared|v1|web",
		// Undeclared: filed under the type the sweep would plan its
		// removal at (the type DIR declares for the kind), no address.
		"ConfigMap smoke-k8s/stray": "kubernetes_config_map_v1||undeclared|v1|",
		"Namespace smoke-k8s":       "kubernetes_namespace|kubernetes_namespace.app|declared|v1|",
		// A custom kind declared through a manifest block meets its object
		// on the natural key read back from the manifest import id.
		"CronTab smoke-k8s/my-crontab": "kubernetes_manifest|kubernetes_manifest.crontab|declared|stable.example.com/v1|web",
		"CronTab smoke-k8s/stray":      "kubernetes_manifest||undeclared|stable.example.com/v1|web",
	}
	if len(got) != len(want) {
		t.Errorf("listed %d object(s), want %d:\n%v", len(got), len(want), got)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}

	// The kind whose list failed is a warning naming the kind, not an
	// error, and not silence.
	var warned bool
	for _, d := range diags {
		if d.Severity() == tfdiags.Warning && d.Description().Summary == "Kubernetes listing incomplete" && strings.Contains(d.Description().Detail, "ServiceAccount") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("the failed ServiceAccount list raised no \"Kubernetes listing incomplete\" warning: %v", diags)
	}
}

func boolWord(declared bool) string {
	if declared {
		return "declared"
	}
	return "undeclared"
}

// TestLiveLsKubernetesList_discoveryFailureIsTheSweepsWarning: a cluster
// that cannot be listed at all is the same warning the sweep raises,
// discovery.SummaryKubernetesSweepUnavailable, no items, and no error -
// the AWS listing's own severity for an unreachable tagging index.
func TestLiveLsKubernetesList_discoveryFailureIsTheSweepsWarning(t *testing.T) {
	sweeper := &liveLsStubSweeper{kindsErr: errors.New("dial tcp 127.0.0.1:6443: connection refused")}
	items, diags := liveLsKubernetesList(context.Background(), "smoke-k8s", sweeper, []string{"kubernetes_namespace"}, "", nil, nil)
	if len(items) != 0 {
		t.Errorf("items = %+v, want none", items)
	}
	if diags.HasErrors() {
		t.Fatalf("an unreachable cluster is an error, want a warning: %s", diags.Err())
	}
	if len(diags) != 1 || diags[0].Description().Summary != discovery.SummaryKubernetesSweepUnavailable {
		t.Fatalf("diags = %v, want exactly one %q warning", diags, discovery.SummaryKubernetesSweepUnavailable)
	}
	if !strings.Contains(diags[0].Description().Detail, "connection refused") {
		t.Errorf("the warning does not carry the cause: %s", diags[0].Description().Detail)
	}
}

// TestLiveLsRung_labelAndManifestSurfacesAreMarkerCarried: a Kubernetes
// type has no tags argument, but it is not declaration-carried - its
// marker is a label, and the cluster listing reads exactly that label. An
// instance of such a type the listing did not find is a genuine absence,
// never a rung, the same answer a taggable AWS type gets.
func TestLiveLsRung_labelAndManifestSurfacesAreMarkerCarried(t *testing.T) {
	labelSurface := &configschema.Block{
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {
				Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"name":   {Type: cty.String, Required: true},
					"labels": {Type: cty.Map(cty.String), Optional: true},
				}},
			},
		},
	}
	manifestSurface := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"manifest": {Type: cty.DynamicPseudoType, Required: true},
		"object":   {Type: cty.DynamicPseudoType, Computed: true},
	}}
	untaggable := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name": {Type: cty.String, Required: true},
	}}
	schemas := map[string]providers.Schema{
		"kubernetes_config_map":           {Block: labelSurface},
		"kubernetes_manifest":             {Block: manifestSurface},
		"aws_iam_group_policy_attachment": {Block: untaggable},
	}

	for _, typeName := range []string{"kubernetes_config_map", "kubernetes_manifest"} {
		res := liveLsTestResolution(t, typeName+".x", identity.ClassConcrete)
		if rung, _, ok := liveLsRung(res, schemas); ok {
			t.Errorf("%s classified as rung %q; a label-carried type not found is an absence, not a rung", typeName, rung)
		}
	}
	// The control: a type with no marker surface of any kind is still the
	// declaration-carried rung this function has always reported.
	res := liveLsTestResolution(t, "aws_iam_group_policy_attachment.x", identity.ClassConcrete)
	if rung, _, ok := liveLsRung(res, schemas); !ok || rung != "declaration-carried" {
		t.Errorf("untaggable AWS type: rung=%q ok=%v, want declaration-carried/true", rung, ok)
	}
}

func liveLsLoadConfig(t *testing.T, hcl string) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o600); err != nil {
		t.Fatal(err)
	}
	loader := configload.NewLoaderForTests(t, false)
	config, _, diags := loader.LoadConfigWithSnapshot(t.Context(), dir, configs.RootModuleCallForTesting())
	if diags.HasErrors() {
		t.Fatal(diags.Error())
	}
	return config
}

// TestLiveLsSubstrates: the substrates are read off the configuration's
// managed resources' providers, and everything that is not a
// configuration - no DIR, a failed load - or names no substrate in
// [substrate.All] is the AWS listing this command has always been.
func TestLiveLsSubstrates(t *testing.T) {
	kubernetesOnly := liveLsLoadConfig(t, `
provider "kubernetes" {}
resource "kubernetes_namespace" "app" {
  metadata { name = "app" }
}
`)
	both := liveLsLoadConfig(t, `
resource "aws_s3_bucket" "data" {}
resource "kubernetes_namespace" "app" {
  metadata { name = "app" }
}
`)
	neither := liveLsLoadConfig(t, `
resource "null_resource" "x" {}
`)
	var loadFailed tfdiags.Diagnostics
	loadFailed = loadFailed.Append(tfdiags.Sourceless(tfdiags.Error, "Unreadable", "not a configuration"))

	awsOnly := liveLsSubstrateSet{sweeps: map[substrate.Sweep]bool{substrate.SweepTaggingIndex: true}}
	kubeOnly := liveLsSubstrateSet{sweeps: map[substrate.Sweep]bool{substrate.SweepLabelList: true}}
	awsAndKube := liveLsSubstrateSet{sweeps: map[substrate.Sweep]bool{substrate.SweepTaggingIndex: true, substrate.SweepLabelList: true}}

	for name, tc := range map[string]struct {
		config *configs.Config
		diags  tfdiags.Diagnostics
		want   liveLsSubstrateSet
	}{
		"no DIR":             {nil, nil, awsOnly},
		"load failed":        {kubernetesOnly, loadFailed, awsOnly},
		"kubernetes only":    {kubernetesOnly, nil, kubeOnly},
		"aws and kubernetes": {both, nil, awsAndKube},
		"neither":            {neither, nil, awsOnly},
	} {
		t.Run(name, func(t *testing.T) {
			if got := liveLsSubstrates(tc.config, tc.diags); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("liveLsSubstrates = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// fakeThirdSubstrate stands in for a family that is neither AWS nor
// Kubernetes - GitHub issue #1583's red proof - registered into
// [substrate.All] only for the duration of
// TestLiveLsSubstratesCoversAThirdSubstrateWithNoNewArm. Its methods
// beyond Name and Sweep are never asked by [liveLsSubstrates]; they exist
// only to satisfy [substrate.Substrate].
type fakeThirdSubstrate struct {
	// Embedded so a method added to substrate.Substrate later reaches this
	// fake without an edit here; the methods below override what the test
	// is about.
	substrate.Substrate
}

func (fakeThirdSubstrate) Name() string                { return "fake" }
func (fakeThirdSubstrate) Surfaces() []markers.Surface { return nil }
func (fakeThirdSubstrate) Sweep() substrate.Sweep      { return substrate.Sweep("fake-sweep") }
func (fakeThirdSubstrate) CarriesAddress() bool        { return false }
func (fakeThirdSubstrate) SurfaceOf(*configschema.Block) (markers.Surface, bool) {
	return "", false
}
func (fakeThirdSubstrate) OwnershipSurfaceOf(*configschema.Block) (markers.Surface, bool) {
	return "", false
}
func (fakeThirdSubstrate) MarkersOf(markers.Surface, cty.Value) (map[string]string, bool) {
	return nil, false
}
func (fakeThirdSubstrate) Writes(markers.Surface) substrate.Writes { return substrate.Writes{} }
func (fakeThirdSubstrate) NewSweeper(cty.Value, bool) (substrate.Sweeper, error) {
	return nil, nil
}
func (fakeThirdSubstrate) CreateCollidesOnKey(markers.Surface) bool { return false }
func (fakeThirdSubstrate) CarrierPhrase(markers.Surface) string     { return "" }
func (fakeThirdSubstrate) NotACarrier(*configschema.Block, string) string {
	return ""
}

// TestLiveLsSubstratesCoversAThirdSubstrateWithNoNewArm is GitHub issue
// #1583's own red proof: a third substrate registered in [substrate.All],
// used by a managed resource and nothing else, must be reflected in
// [liveLsSubstrates]'s answer - not silently folded into the AWS default,
// which is what a switch naming only [substrate.AWS] and
// [substrate.Kubernetes] does to anything else. Before the fix, this test
// does not compile: liveLsSubstrateSet had an aws/kubernetes bool pair
// with nowhere to record a third family at all.
func TestLiveLsSubstratesCoversAThirdSubstrateWithNoNewArm(t *testing.T) {
	orig := substrate.All
	substrate.All = append(append([]substrate.Substrate{}, orig...), fakeThirdSubstrate{Substrate: substrate.AWS})
	t.Cleanup(func() { substrate.All = orig })

	config := liveLsLoadConfig(t, `
resource "fake_thing" "x" {}
`)
	got := liveLsSubstrates(config, nil)
	if !got.has(fakeThirdSubstrate{Substrate: substrate.AWS}.Sweep()) {
		t.Errorf("liveLsSubstrates(config with only a fake_thing resource) = %+v, want the fake substrate's sweep listed", got)
	}
	if got.has(substrate.SweepTaggingIndex) {
		t.Errorf("liveLsSubstrates(config with only a fake_thing resource) = %+v, want no AWS tagging-index listing (there is no aws provider here)", got)
	}
}

// TestLiveLsCommand_Run_kubernetesOnlyConfigurationNeverTouchesAWS: with a
// Kubernetes-only DIR, the AWS passes do not run at all - no tagging call,
// no IAM call, no warning about either - and the command exits 0. This
// package installs no kubernetes plugin, so the comparison skips before
// the cluster listing; the listing itself is TestLiveLsKubernetesList's
// and the smoke scenario's.
func TestLiveLsCommand_Run_kubernetesOnlyConfigurationNeverTouchesAWS(t *testing.T) {
	// A fake AWS endpoint that fails the test if anything reaches it.
	srv := &fakeLiveLsServer{t: t}
	server := srv.start()
	t.Setenv("TOFU_LIVE_CLOUDCONTROL", "")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	touched := false
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		touched = true
		http.Error(w, "the AWS listing ran against a Kubernetes-only configuration", http.StatusInternalServerError)
	})

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
provider "kubernetes" {}
resource "kubernetes_namespace" "app" {
  metadata { name = "app" }
}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	// WorkingDir "." for the reason liveLsJSONRun gives: DIR resolves
	// the way it does on a command line.
	view, done := testView(t)
	c := &LiveLsCommand{Meta: Meta{WorkingDir: workdir.NewDir("."), View: view}}
	code := c.Run([]string{"-no-color", "-estate=app", dir})
	out := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
	}
	if touched {
		t.Error("the AWS endpoint was called for a configuration with no aws provider")
	}
	if strings.Contains(out.Stdout()+out.Stderr(), "Listing disabled") || strings.Contains(out.Stdout()+out.Stderr(), "Tagging index unavailable") {
		t.Errorf("an AWS warning was raised for a Kubernetes-only configuration:\n%s\n%s", out.Stdout(), out.Stderr())
	}
	if !strings.Contains(out.Stdout(), `Estate "app": 0 resource(s) carry its marker.`) {
		t.Errorf("the listing header is missing or counts something:\n%s", out.Stdout())
	}
}

// TestLiveLsKubernetesListsControllerHeld (GitHub issue #1607): an object a
// Helm release holds is listed, with its release, and never under a
// block's address unless a block names it.
func TestLiveLsKubernetesListsControllerHeld(t *testing.T) {
	cm := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map_v1"}}
	sweeper := &liveLsStubSweeper{
		kinds: []kubesweep.Kind{cm},
		held: map[string][]kubesweep.HeldObject{
			"ConfigMap": {{Kind: "ConfigMap", Namespace: "web", Name: "web-greeting", Labels: map[string]string{"tofu-estate": "smoke-k8s"}, HeldBy: "Helm release web/web"}},
		},
	}
	items, diags := liveLsKubernetesList(context.Background(), "smoke-k8s", sweeper, []string{"kubernetes_config_map_v1"}, "", nil, nil)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v, want the held object", items)
	}
	it := items[0]
	if it.ID != "web/web-greeting" || it.HeldBy != "Helm release web/web" || it.Declared || it.Address != "" || it.Tags["tofu-estate"] != "smoke-k8s" {
		t.Errorf("item = %+v", it)
	}
}

// TestLiveLsKubernetesList_BindsByAddressAnnotation (GitHub issue #1677):
// live-ls must read the same address-annotation join the plan's Kubernetes
// sweep binds by ([discovery.KubernetesAddressBindings], from GitHub issue
// #1640), not the natural-key join alone. Two shapes, #1640's own:
//
//   - #1541's: the configuration renamed the object. The resolution is
//     concrete and names the NEW object (not yet listed); the OLD object is
//     still on the cluster, carrying the annotation of the block that
//     declared it. Needs nothing beyond req.Resolutions.
//   - #1539's: the static evaluator refused the instance and the plan-node
//     seam took it over, so it never reaches req.Resolutions at all - only
//     nodeRefused says the address is declared. Proven both ways: bound
//     when the caller passes nodeRefused, still an orphan when it does not,
//     so the wiring from liveLsGaps is what a reader can trust, not an
//     accident of addressCandidate's other conditions.
func TestLiveLsKubernetesList_BindsByAddressAnnotation(t *testing.T) {
	cm := kubesweep.Kind{GVR: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Kind: "ConfigMap", Namespaced: true, APIVersion: "v1", TypeNames: []string{"kubernetes_config_map_v1"}}
	types := []string{"kubernetes_config_map_v1"}

	t.Run("renamed object binds from a resolution alone", func(t *testing.T) {
		sweeper := &liveLsStubSweeper{
			kinds: []kubesweep.Kind{cm},
			objects: map[string][]kubesweep.Object{
				"ConfigMap": {{
					Kind: "ConfigMap", Namespace: "rep-chdf", Name: "cfg-a",
					ImportID: "rep-chdf/cfg-a",
					Labels:   map[string]string{"tofu-estate": "smoke-k8s"},
					Address:  "kubernetes_config_map_v1.cfg",
				}},
			},
		}
		// The resolution names the new object, cfg-b, which nothing has
		// listed yet - stock's own create-before-destroy shape.
		resolutions := []identity.Resolution{liveLsK8sResolution(t, "kubernetes_config_map_v1", "cfg", "rep-chdf/cfg-b")}

		items, diags := liveLsKubernetesList(context.Background(), "smoke-k8s", sweeper, types, "", resolutions, nil)
		if diags.HasErrors() {
			t.Fatalf("unexpected errors: %s", diags.Err())
		}
		if len(items) != 1 {
			t.Fatalf("items = %+v, want the one renamed object", items)
		}
		it := items[0]
		if !it.Declared || it.Address != "kubernetes_config_map_v1.cfg" || it.Type != "kubernetes_config_map_v1" {
			t.Errorf("cfg-a = %+v, want declared at kubernetes_config_map_v1.cfg", it)
		}
	})

	t.Run("node-refused instance binds only when the caller hands its refusal in", func(t *testing.T) {
		newSweeper := func() *liveLsStubSweeper {
			return &liveLsStubSweeper{
				kinds: []kubesweep.Kind{cm},
				objects: map[string][]kubesweep.Object{
					"ConfigMap": {{
						Kind: "ConfigMap", Namespace: "m1116-res", Name: "my-awesome-cron-image-reader",
						ImportID: "m1116-res/my-awesome-cron-image-reader",
						Labels:   map[string]string{"tofu-estate": "smoke-k8s"},
						Address:  "kubernetes_config_map_v1.reader",
					}},
				},
			}
		}
		nodeRefused := map[string]bool{"kubernetes_config_map_v1.reader": true}

		items, diags := liveLsKubernetesList(context.Background(), "smoke-k8s", newSweeper(), types, "", nil, nodeRefused)
		if diags.HasErrors() {
			t.Fatalf("unexpected errors: %s", diags.Err())
		}
		if len(items) != 1 {
			t.Fatalf("items = %+v, want the one object", items)
		}
		if it := items[0]; !it.Declared || it.Address != "kubernetes_config_map_v1.reader" {
			t.Errorf("with nodeRefused: item = %+v, want bound at kubernetes_config_map_v1.reader", it)
		}

		// The control: with no resolution and no nodeRefused entry, nothing
		// says the annotation names a declared instance, so the object stays
		// exactly the orphan it always was.
		items, diags = liveLsKubernetesList(context.Background(), "smoke-k8s", newSweeper(), types, "", nil, nil)
		if diags.HasErrors() {
			t.Fatalf("unexpected errors: %s", diags.Err())
		}
		if it := items[0]; it.Declared || it.Address != "" {
			t.Errorf("without nodeRefused: item = %+v, want undeclared", it)
		}
	})
}
