// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package untag

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1656: undeclared_tagged = "untag" on a manifest-declared
// Kubernetes orphan. Ruled 2026-09-27: the release is a merge patch
// deleting tofu-estate, sent dry-run first and diffed the way live-import's
// adoption patch is. Before, #1644 refused it by name and the label stayed
// on the object for every later sweep to find.

const crontabID = "apiVersion=stable.example.com/v1,kind=CronTab,namespace=orphans,name=stale"

func crontabTarget() Target {
	return Target{TypeName: "kubernetes_manifest", ImportID: crontabID, Marker: "smoke-crd"}
}

func liveCrontab(labels map[string]string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "stable.example.com/v1",
		"kind":       "CronTab",
		"metadata":   map[string]any{"name": "stale", "namespace": "orphans", "resourceVersion": "41"},
		"spec":       map[string]any{"cronSpec": "* * * * */5", "replicas": int64(3)},
	}}
	if labels != nil {
		u.SetLabels(labels)
	}
	return u
}

// fakeReleaser is one live object behind the [kubesweep.LabelReleaser]
// interface. dryHook, when set, rewrites what a dry run answers with, the
// way a mutating admission webhook would.
type fakeReleaser struct {
	live     *unstructured.Unstructured
	reads    []kubesweep.ObjectRef
	calls    []releaseCall
	dryHook  func(*unstructured.Unstructured)
	rejectAs string
}

type releaseCall struct {
	ref    kubesweep.ObjectRef
	keys   []string
	dryRun bool
}

func (f *fakeReleaser) ReadObject(_ context.Context, ref kubesweep.ObjectRef) (*unstructured.Unstructured, bool, error) {
	f.reads = append(f.reads, ref)
	if f.live == nil {
		return nil, false, nil
	}
	return f.live.DeepCopy(), true, nil
}

func (f *fakeReleaser) DeleteLabels(_ context.Context, ref kubesweep.ObjectRef, keys []string, _ string, dryRun bool) (*unstructured.Unstructured, string, error) {
	f.calls = append(f.calls, releaseCall{ref: ref, keys: keys, dryRun: dryRun})
	if f.rejectAs != "" {
		return nil, f.rejectAs, nil
	}
	next := f.live.DeepCopy()
	labels := next.GetLabels()
	for _, k := range keys {
		delete(labels, k)
	}
	next.SetLabels(labels)
	next.SetResourceVersion("42")
	if dryRun {
		if f.dryHook != nil {
			f.dryHook(next)
		}
		return next, "", nil
	}
	f.live = next
	return next.DeepCopy(), "", nil
}

func (f *fakeReleaser) writes() int {
	n := 0
	for _, c := range f.calls {
		if !c.dryRun {
			n++
		}
	}
	return n
}

func manifestProvider() *fakeCluster {
	return newFakeCluster("kubernetes_manifest", manifestSchema(), cty.NullVal(cty.DynamicPseudoType))
}

func TestRelease_ManifestSurfaceReleasesTheEstateLabelThroughAPatch(t *testing.T) {
	p := manifestProvider()
	k := &fakeReleaser{live: liveCrontab(map[string]string{"app": "cron", markers.TagEstate: "smoke-crd"})}

	res, diags := Release(context.Background(), p, k, testKey, []Target{crontabTarget()})
	out := res.Outcomes[0]
	if !out.OK || diags.HasErrors() {
		t.Fatalf("outcome = %s (diags %v), want RELEASED", out, diags.Err())
	}
	wantRef := kubesweep.ObjectRef{APIVersion: "stable.example.com/v1", Kind: "CronTab", Namespace: "orphans", Name: "stale"}
	if len(k.calls) != 2 || !k.calls[0].dryRun || k.calls[1].dryRun {
		t.Fatalf("calls = %+v, want a dry run then one write", k.calls)
	}
	for _, c := range k.calls {
		if c.ref != wantRef || !reflect.DeepEqual(c.keys, []string{markers.TagEstate}) {
			t.Errorf("call = %+v, want %s with keys [%s]", c, wantRef, markers.TagEstate)
		}
	}
	if got := k.live.GetLabels(); !reflect.DeepEqual(got, map[string]string{"app": "cron"}) {
		t.Errorf("labels after release = %v, want app=cron alone", got)
	}
	if p.ImportResourceStateCalled || p.applied != 0 {
		t.Errorf("the provider was used for a manifest release: imported %v, applied %d", p.ImportResourceStateCalled, p.applied)
	}
	if out.Detail != `Released "tofu-estate". This resource is no longer managed by this estate.` {
		t.Errorf("detail = %q", out.Detail)
	}
}

func TestRelease_ManifestSurfaceAlreadyReleasedWritesNothing(t *testing.T) {
	k := &fakeReleaser{live: liveCrontab(map[string]string{"app": "cron"})}
	res, _ := Release(context.Background(), manifestProvider(), k, testKey, []Target{crontabTarget()})
	if out := res.Outcomes[0]; !out.OK || len(k.calls) != 0 {
		t.Fatalf("outcome = %s, patches %d; want OK with no patch", out, len(k.calls))
	}
}

func TestRelease_ManifestSurfaceGoneIsNothingToRelease(t *testing.T) {
	k := &fakeReleaser{}
	res, _ := Release(context.Background(), manifestProvider(), k, testKey, []Target{crontabTarget()})
	if out := res.Outcomes[0]; !out.OK || len(k.calls) != 0 || !strings.Contains(out.Detail, "no longer exists") {
		t.Fatalf("outcome = %s, patches %d; want OK, nothing sent", out, len(k.calls))
	}
}

// A dry-run answer that differs from the live object anywhere but the one
// released key is refused before the real write: the spec, and any other
// label.
func TestRelease_ManifestSurfaceRefusesADryRunThatChangesMoreThanTheLabel(t *testing.T) {
	cases := map[string]func(*unstructured.Unstructured){
		"spec.replicas": func(u *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(u.Object, int64(1), "spec", "replicas")
		},
		"metadata.labels.app": func(u *unstructured.Unstructured) {
			l := u.GetLabels()
			delete(l, "app")
			u.SetLabels(l)
		},
		"metadata.labels.injected": func(u *unstructured.Unstructured) {
			l := u.GetLabels()
			l["injected"] = "yes"
			u.SetLabels(l)
		},
	}
	for name, hook := range cases {
		t.Run(name, func(t *testing.T) {
			k := &fakeReleaser{live: liveCrontab(map[string]string{"app": "cron", markers.TagEstate: "smoke-crd"}), dryHook: hook}
			res, _ := Release(context.Background(), manifestProvider(), k, testKey, []Target{crontabTarget()})
			out := res.Outcomes[0]
			if out.OK || k.writes() != 0 {
				t.Fatalf("outcome = %s, writes %d; want a refusal after the dry run alone", out, k.writes())
			}
			if !strings.Contains(out.Detail, name) {
				t.Errorf("the refusal does not name %s: %s", name, out.Detail)
			}
			if _, still := k.live.GetLabels()[markers.TagEstate]; !still {
				t.Error("the label was removed despite the refusal")
			}
		})
	}
}

func TestRelease_ManifestSurfaceServerRejectionWritesNothing(t *testing.T) {
	k := &fakeReleaser{live: liveCrontab(map[string]string{markers.TagEstate: "smoke-crd"}), rejectAs: "bob may not remove tofu-estate"}
	res, _ := Release(context.Background(), manifestProvider(), k, testKey, []Target{crontabTarget()})
	out := res.Outcomes[0]
	if out.OK || len(k.calls) != 1 || !k.calls[0].dryRun {
		t.Fatalf("outcome = %s, calls %+v; want one refused dry run", out, k.calls)
	}
	if !strings.Contains(out.Detail, "bob may not remove tofu-estate") {
		t.Errorf("the refusal does not carry the server's words: %s", out.Detail)
	}
}

// With no cluster client for the provider configuration, the release is
// still refused by name with the kubectl write that makes it, reading and
// changing nothing.
func TestRelease_ManifestSurfaceWithNoClusterClientIsRefusedByName(t *testing.T) {
	p := manifestProvider()
	res, _ := Release(context.Background(), p, nil, testKey, []Target{crontabTarget()})
	out := res.Outcomes[0]
	if out.OK || p.applied != 0 || p.ImportResourceStateCalled {
		t.Fatalf("outcome = %s; want a refusal that touches nothing", out)
	}
	for _, want := range []string{"kubectl label", "tofu-estate-", "kind=CronTab", "no cluster client"} {
		if !strings.Contains(out.Detail, want) {
			t.Errorf("the refusal does not carry %q: %s", want, out.Detail)
		}
	}
}

func TestRelease_ManifestSurfaceUnreadableImportIDIsRefused(t *testing.T) {
	k := &fakeReleaser{live: liveCrontab(map[string]string{markers.TagEstate: "smoke-crd"})}
	res, _ := Release(context.Background(), manifestProvider(), k, testKey, []Target{{TypeName: "kubernetes_manifest", ImportID: "orphans/stale"}})
	if out := res.Outcomes[0]; out.OK || len(k.reads) != 0 || len(k.calls) != 0 {
		t.Fatalf("outcome = %s, reads %d, patches %d; want a refusal before any request", out, len(k.reads), len(k.calls))
	}
}
