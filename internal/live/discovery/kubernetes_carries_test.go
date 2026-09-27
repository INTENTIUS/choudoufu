// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1641 (#1605 step 3): substrate.Kubernetes.CarriesAddress
// is true, so #1617's refusal stands only where the sweep found an object
// of the refused instance's type that carries no address annotation. The
// sweep's half is Verdicts.KubernetesUnaddressed.

func TestKubernetesSweepAccountsUnaddressedObjects(t *testing.T) {
	reader := k8sInstance(t, "kubernetes_config_map_v1", "reader")
	manifest := k8sInstance(t, "kubernetes_manifest", "cm")
	gone := k8sInstance(t, "kubernetes_config_map_v1", "gone")
	secretKind := configMapKind()
	secretKind.Kind, secretKind.GVR.Resource, secretKind.TypeNames = "Secret", "secrets", []string{"kubernetes_secret_v1"}

	type want struct {
		present bool
		objects []string
	}
	for _, tc := range []struct {
		name     string
		kinds    []kubesweep.Kind
		objects  map[string][]kubesweep.Object
		failKind string
		refused  addrs.AbsResourceInstance
		scope    addrs.AbsProviderConfig
		want     want
	}{
		{
			// #1539's first apply: nothing exists yet, so a create is
			// safe and the object it makes will carry the annotation.
			name:    "greenfield: nothing listed",
			kinds:   []kubesweep.Kind{configMapKind()},
			refused: reader,
			want:    want{present: true},
		},
		{
			// A migrated estate before live-import stamped the
			// annotation: this object may be the block's.
			name:    "an unannotated object of the kind",
			kinds:   []kubesweep.Kind{configMapKind()},
			objects: map[string][]kubesweep.Object{"ConfigMap": {labelled("ConfigMap", "m1116-res", "my-awesome-cron-image-reader", "")}},
			refused: reader,
			want:    want{present: true, objects: []string{"ConfigMap m1116-res/my-awesome-cron-image-reader"}},
		},
		{
			name:    "an annotation that does not parse is no annotation",
			kinds:   []kubesweep.Kind{configMapKind()},
			objects: map[string][]kubesweep.Object{"ConfigMap": {labelled("ConfigMap", "ns", "x", "not an address")}},
			refused: reader,
			want:    want{present: true, objects: []string{"ConfigMap ns/x"}},
		},
		{
			// A deleted block's object names its own block: it is an
			// orphan, and not this block's.
			name:    "an object annotated for another block",
			kinds:   []kubesweep.Kind{configMapKind()},
			objects: map[string][]kubesweep.Object{"ConfigMap": {labelled("ConfigMap", "ns", "left", gone.String())}},
			refused: reader,
			want:    want{present: true},
		},
		{
			name:    "an unannotated object of a kind the type does not manage",
			kinds:   []kubesweep.Kind{configMapKind(), secretKind},
			objects: map[string][]kubesweep.Object{"Secret": {labelled("Secret", "ns", "s", "")}},
			refused: reader,
			want:    want{present: true},
		},
		{
			name:  "a terminating unannotated object",
			kinds: []kubesweep.Kind{configMapKind()},
			objects: map[string][]kubesweep.Object{"ConfigMap": {func() kubesweep.Object {
				o := labelled("ConfigMap", "ns", "x", "")
				o.DeletionTimestamp = "2026-09-26T00:00:00Z"
				return o
			}()}},
			refused: reader,
			want:    want{present: true},
		},
		{
			// A manifest block can declare any kind.
			name:    "a manifest block and an unannotated object of a built-in kind",
			kinds:   []kubesweep.Kind{configMapKind(), secretKind},
			objects: map[string][]kubesweep.Object{"Secret": {labelled("Secret", "ns", "s", "")}},
			refused: manifest,
			want:    want{present: true, objects: []string{"Secret ns/s"}},
		},
		{
			// The leg cannot say what it did not list: the refusal stands.
			name:     "the kind failed to list",
			kinds:    []kubesweep.Kind{configMapKind()},
			failKind: "ConfigMap",
			refused:  reader,
			want:     want{present: false},
		},
		{
			name:     "a manifest block, and any kind failed to list",
			kinds:    []kubesweep.Kind{configMapKind(), secretKind},
			failKind: "Secret",
			refused:  manifest,
			want:     want{present: false},
		},
		{
			name:    "the instance is another pass's",
			kinds:   []kubesweep.Kind{configMapKind()},
			refused: k8sInstance(t, "kubernetes_config_map_v1", "cfg"),
			scope:   addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes"), Alias: "other"},
			want:    want{present: false},
		},
		{
			name:    "the instance's type is not a Kubernetes type",
			kinds:   []kubesweep.Kind{configMapKind()},
			refused: k8sInstance(t, "aws_s3_bucket", "b"),
			want:    want{present: false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sweeper := &stubSweeper{kinds: tc.kinds, objects: tc.objects, failKind: tc.failKind}
			req := Request{
				Estate:        "m1116",
				Sweepers:      []Sweeper{KubernetesSweep{Client: sweeper, Types: append(k8sTypes(), "kubernetes_secret_v1"), ManifestType: "kubernetes_manifest"}},
				NodeRefused:   map[string]bool{tc.refused.String(): true},
				ScopeProvider: tc.scope,
				Config:        k8sScopeConfig(t),
			}
			res := &Result{}
			if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
				t.Fatalf("unexpected errors: %s", diags.Err())
			}
			got, present := res.KubernetesUnaddressed[tc.refused.String()]
			if present != tc.want.present || !reflect.DeepEqual(nonNil(got), nonNil(tc.want.objects)) {
				t.Fatalf("KubernetesUnaddressed[%s] = %q (present %v), want %q (present %v)", tc.refused, got, present, tc.want.objects, tc.want.present)
			}
		})
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// An instance the annotation binds is not in the account: the marker
// index answers for it at the node.
func TestKubernetesSweepBoundInstanceIsNotUnaddressed(t *testing.T) {
	reader := k8sInstance(t, "kubernetes_config_map_v1", "reader")
	req := issue1539Request(issue1539Sweeper(markers.EscapeAddress(reader.String())))
	res := &Result{}
	res.Resolutions = append(res.Resolutions, req.Resolutions...)
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if _, present := res.KubernetesUnaddressed[reader.String()]; present {
		t.Fatalf("KubernetesUnaddressed = %v; %s was bound", res.KubernetesUnaddressed, reader)
	}
}

// TestKubernetesSweepUnaddressedDecidesTheNodeRefusal is #1539's
// reproduction end to end through the sweep and the node, at its first
// apply (nothing exists) and over a migrated object that lacks the
// annotation. Before #1641 the first refused too, so no object carrying
// the annotation could ever come to exist for #1640 to bind.
func TestKubernetesSweepUnaddressedDecidesTheNodeRefusal(t *testing.T) {
	reader := k8sInstance(t, "kubernetes_config_map_v1", "reader")
	var refusal tfdiags.Diagnostics
	refusal = refusal.Append(tfdiags.Sourceless(tfdiags.Error, "Identity not resolvable from configuration", "reader.name refers to kubernetes_manifest.crontab.object.spec.image"))

	for _, tc := range []struct {
		name       string
		configMaps []kubesweep.Object
		wantRefuse bool
	}{
		{"first apply: nothing listed, the create is planned", nil, false},
		{"an unannotated ConfigMap could be the block's: the refusal stands", []kubesweep.Object{labelled("ConfigMap", "m1116-res", "my-awesome-cron-image-reader", "")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sweeper := issue1539Sweeper("")
			sweeper.objects["ConfigMap"] = tc.configMaps
			req := issue1539Request(sweeper)
			res := &Result{}
			res.Resolutions = append(res.Resolutions, req.Resolutions...)
			if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
				t.Fatalf("unexpected errors: %s", diags.Err())
			}
			node := &projection.NodeResolver{
				MarkerIndex:        projection.NewMarkerIndex(res.Resolutions),
				StaticRefusals:     map[string]tfdiags.Diagnostics{reader.String(): refusal},
				UnaddressedObjects: res.KubernetesUnaddressed,
			}
			value := readerValue()
			if !tc.wantRefuse {
				value = cty.ObjectVal(map[string]cty.Value{
					"data": cty.NullVal(cty.Map(cty.String)),
					"id":   cty.NullVal(cty.String),
					"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
						"annotations": cty.NullVal(cty.Map(cty.String)),
						"labels":      cty.NullVal(cty.Map(cty.String)),
						"name":        cty.UnknownVal(cty.String),
						"namespace":   cty.StringVal("m1116-res"),
					})}),
				})
			}
			_, found, diags := node.ResolveResourceIdentity(context.Background(), reader, value, configMapSchema())
			if found {
				t.Fatalf("found an object; nothing is bound to %s", reader)
			}
			refused := false
			for _, d := range diags {
				if d.Severity() == tfdiags.Error && d.Description().Summary == projection.SummaryIdentityUnresolvedNoAddress {
					refused = true
					if !strings.Contains(d.Description().Detail, "ConfigMap m1116-res/my-awesome-cron-image-reader") {
						t.Errorf("the refusal does not name the unannotated object: %s", d.Description().Detail)
					}
				}
			}
			if refused != tc.wantRefuse {
				t.Fatalf("refused = %v, want %v; diags: %v", refused, tc.wantRefuse, diags.ErrWithWarnings())
			}
		})
	}
}

// TestKubernetesSweepTwoClaimantsAreACollision is the case #1640 carried
// forward: two listed objects whose annotations name one declared
// instance, neither at the namespace and name its configuration names.
// #1640 left both as orphans, so the plan destroyed both. The crash window
// of a create_before_destroy replacement produces exactly this: the
// replacement is created carrying the block's address, and the apply stops
// before the old object is destroyed. When the block's name is one the
// node reads (#1539's shape), the configuration names neither object, and
// a double destroy would take the replacement with the old object while
// the node planned a create at the replacement's own name. So it is the
// collision refusal AWS raises for two objects carrying one tofu-address.
func TestKubernetesSweepTwoClaimantsAreACollision(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map_v1", "cfg")
	for _, tc := range []struct {
		name        string
		resolutions []identity.Resolution
		refused     map[string]bool
	}{
		{
			name:    "the node reads the name (#1539's shape), after an interrupted replace",
			refused: map[string]bool{cfg.String(): true},
		},
		{
			name:        "the configuration names a third object, after an interrupted replace",
			resolutions: []identity.Resolution{{Addr: cfg, Class: identity.ClassConcrete, ImportID: "ns/cfg-c", IdentityValues: map[string]string{"name": "cfg-c", "namespace": "ns"}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sweeper := &stubSweeper{kinds: []kubesweep.Kind{configMapKind()}, objects: map[string][]kubesweep.Object{"ConfigMap": {
				labelled("ConfigMap", "ns", "cfg-a", cfg.String()),
				labelled("ConfigMap", "ns", "cfg-b", cfg.String()),
			}}}
			req := Request{
				Estate:      "m1116",
				Sweepers:    []Sweeper{KubernetesSweep{Client: sweeper, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
				Resolutions: tc.resolutions,
				NodeRefused: tc.refused,
			}
			res := &Result{}
			res.Resolutions = append(res.Resolutions, req.Resolutions...)
			diags := sweepKubernetes(context.Background(), req, res)

			if len(res.Orphans) != 0 {
				t.Errorf("orphans = %v; neither object may be proposed for destruction while which one is the block's is unknown", res.Orphans)
			}
			if len(res.Bindings) != 0 || len(res.KubernetesAddressBound) != 0 {
				t.Errorf("bound %v / %v; the annotation cannot say which object is the block's", res.Bindings, res.KubernetesAddressBound)
			}
			var collision tfdiags.Diagnostic
			for _, d := range diags {
				if d.Description().Summary == problemSummaries[ProblemCollision] {
					collision = d
				}
			}
			if collision == nil || collision.Severity() != tfdiags.Error {
				t.Fatalf("want an error %q, got: %v", problemSummaries[ProblemCollision], diags.ErrWithWarnings())
			}
			detail := collision.Description().Detail
			for _, want := range []string{"ns/cfg-a", "ns/cfg-b", cfg.String(), markers.AddressAnnotation} {
				if !strings.Contains(detail, want) {
					t.Errorf("the refusal does not name %q: %s", want, detail)
				}
			}
			var problem *Problem
			for i := range res.Problems {
				if res.Problems[i].Kind == ProblemCollision {
					problem = &res.Problems[i]
				}
			}
			if problem == nil || problem.Addr.String() != cfg.String() || !reflect.DeepEqual(problem.LiveIDs, []string{"ns/cfg-a", "ns/cfg-b"}) {
				t.Errorf("problem = %+v, want a collision at %s over ns/cfg-a and ns/cfg-b", problem, cfg)
			}
		})
	}
}

// TestMergeKeepsTheUnaddressedAccount: a run with an AWS provider beside
// the Kubernetes one merges its passes, and the Kubernetes pass's account
// must survive, empty entries included - an empty entry is what lets the
// node plan a create.
func TestMergeKeepsTheUnaddressedAccount(t *testing.T) {
	kube := &Result{}
	kube.KubernetesUnaddressed = map[string][]string{
		"kubernetes_config_map_v1.reader": {},
		"kubernetes_config_map_v1.other":  {"ConfigMap ns/x"},
	}
	awsPass := Pass{Provider: testProviderAddr(t, ""), Result: &Result{}}
	kubePass := Pass{Provider: addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}, Result: kube}
	merged, _, diags := Merge(estateName, []Pass{awsPass, kubePass}, false)
	assertNoErrors(t, diags)
	got := merged.UnaddressedAccount()
	if objects, ok := got["kubernetes_config_map_v1.reader"]; !ok || len(objects) != 0 {
		t.Errorf("reader = %q (present %v), want present and empty", objects, ok)
	}
	if !reflect.DeepEqual(got["kubernetes_config_map_v1.other"], []string{"ConfigMap ns/x"}) {
		t.Errorf("other = %q", got["kubernetes_config_map_v1.other"])
	}
	var none *Result
	if none.UnaddressedAccount() != nil {
		t.Error("a nil result has an account")
	}
}

// TestKubernetesAddressBindingsAgreesOnTwoClaimants: live-ls asks the same
// rule the sweep does (GitHub issue #1677). Where the sweep refuses two
// claimants as a collision and binds neither (#1641), live-ls must not
// report either as bound.
func TestKubernetesAddressBindingsAgreesOnTwoClaimants(t *testing.T) {
	cfg := k8sInstance(t, "kubernetes_config_map_v1", "cfg")
	undeclared := []UndeclaredObject{
		{Kind: configMapKind(), TypeName: "kubernetes_config_map_v1", Object: labelled("ConfigMap", "ns", "cfg-a", cfg.String())},
		{Kind: configMapKind(), TypeName: "kubernetes_config_map_v1", Object: labelled("ConfigMap", "ns", "cfg-b", cfg.String())},
	}
	listed := ListedObjects{}
	listed.Add("ConfigMap", "ns/cfg-a")
	listed.Add("ConfigMap", "ns/cfg-b")
	req := Request{Estate: "m1116", NodeRefused: map[string]bool{cfg.String(): true}}
	declared := DeclaredKubernetesObjects(nil, k8sTypes(), "kubernetes_manifest")
	if got := KubernetesAddressBindings(req, "kubernetes_manifest", declared, listed, undeclared); len(got) != 0 {
		t.Fatalf("live-ls binds %v; the sweep binds neither of two claimants", got)
	}
	// And one claimant alone binds in both.
	if got := KubernetesAddressBindings(req, "kubernetes_manifest", declared, listed, undeclared[:1]); got[0].String() != cfg.String() {
		t.Fatalf("live-ls binds %v, want index 0 at %s", got, cfg)
	}
}
