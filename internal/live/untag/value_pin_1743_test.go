// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package untag

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1743, half (b). An untag target is an orphan discovery saw
// carrying this estate's label. Between that sweep and the release in
// AfterApply, live-mv can move the object to another estate, which rewrites
// the same label key to the other estate's name. A release that checks only
// that the key is present then deletes the other estate's label, and the
// object belongs to nobody.
//
// The estate in these tests is the one the neighbouring tests use
// ("smoke-crd", "smoke-k8s"); the other estate is otherEstate.

const otherEstate = "estate-b"

// The value check on the manifest path: the label now names another estate,
// so there is nothing of this estate's to release.
func TestRelease_ManifestSurfaceLeavesAnotherEstatesLabel(t *testing.T) {
	k := &fakeReleaser{live: liveCrontabAnnotated(
		map[string]string{"app": "cron", markers.TagEstate: otherEstate},
		map[string]string{markers.AddressAnnotation: "kubernetes_manifest.cron"})}

	_, diags := Release(context.Background(), manifestProvider(), k, testKey, []Target{crontabTarget()})
	if diags.HasErrors() {
		t.Fatalf("diags: %v", diags.Err())
	}
	if len(k.calls) != 0 {
		t.Fatalf("sent %d patch(es) to an object whose label names %q, not this estate: %+v", len(k.calls), otherEstate, k.calls)
	}
	if got := k.live.GetLabels()[markers.TagEstate]; got != otherEstate {
		t.Fatalf("%s = %q after the release, want %q left alone", markers.TagEstate, got, otherEstate)
	}
}

// The value check on the provider-plan path (labels here; tags share the
// same check in releaseOne).
func TestRelease_LabelSurfaceLeavesAnotherEstatesLabel(t *testing.T) {
	c := newFakeCluster("kubernetes_config_map_v1", configMapSchema(),
		configMapObject(map[string]string{"app": "web", markers.TagEstate: otherEstate}))

	_, diags := Release(context.Background(), c, nil, testKey, []Target{configMapTarget()})
	if diags.HasErrors() {
		t.Fatalf("diags: %v", diags.Err())
	}
	if c.applied != 0 {
		t.Fatalf("applied %d write(s) to an object whose label names %q, not this estate", c.applied, otherEstate)
	}
	got, _ := markers.LabelsOf(c.live)
	if got[markers.TagEstate] != otherEstate {
		t.Fatalf("%s = %q after the release, want %q left alone", markers.TagEstate, got[markers.TagEstate], otherEstate)
	}
}

// The pin. The value check reads the object once; live-mv can still land
// between that read and the patch. The patch must carry the resourceVersion
// (and uid) it was checked against, so the API server refuses it with a
// conflict if the object changed in between.
//
// pinCluster is a one-object API server behind client-go's fake dynamic
// client: get, and merge patch with the server's optimistic-concurrency
// rule (a patch naming a metadata.resourceVersion or metadata.uid the
// stored object does not carry is a 409 Conflict), dryRun honoured, and
// every real write bumping the resourceVersion. It records each patch body.
type pinCluster struct {
	obj     *unstructured.Unstructured
	rv      int
	patches []map[string]any
	// beforeWrite runs once, just before the first non-dry-run patch is
	// judged: the other writer landing in the window.
	beforeWrite func(*pinCluster)
}

var pinGVR = schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}

func (p *pinCluster) bump() {
	p.rv++
	p.obj.SetResourceVersion(strconv.Itoa(p.rv))
}

func mergeInto(dst, patch map[string]any) {
	for k, v := range patch {
		if v == nil {
			delete(dst, k)
			continue
		}
		if pm, ok := v.(map[string]any); ok {
			dm, ok := dst[k].(map[string]any)
			if !ok {
				dm = map[string]any{}
			}
			mergeInto(dm, pm)
			dst[k] = dm
			continue
		}
		dst[k] = v
	}
}

func (p *pinCluster) client(t *testing.T) *kubesweep.Client {
	t.Helper()
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{pinGVR: "CronTabList"})
	dyn.PrependReactor("get", "crontabs", func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, p.obj.DeepCopy(), nil
	})
	dyn.PrependReactor("patch", "crontabs", func(a clienttesting.Action) (bool, runtime.Object, error) {
		pa := a.(clienttesting.PatchActionImpl)
		var body map[string]any
		if err := json.Unmarshal(pa.GetPatch(), &body); err != nil {
			return true, nil, err
		}
		p.patches = append(p.patches, body)
		dry := len(pa.PatchOptions.DryRun) > 0
		if !dry && p.beforeWrite != nil {
			p.beforeWrite(p)
			p.beforeWrite = nil
		}
		meta, _ := body["metadata"].(map[string]any)
		if rv, ok := meta["resourceVersion"].(string); ok && rv != p.obj.GetResourceVersion() {
			return true, nil, apierrors.NewConflict(pinGVR.GroupResource(), p.obj.GetName(), errOptimisticLock)
		}
		if uid, ok := meta["uid"].(string); ok && uid != string(p.obj.GetUID()) {
			return true, nil, apierrors.NewConflict(pinGVR.GroupResource(), p.obj.GetName(), errOptimisticLock)
		}
		next := p.obj.DeepCopy()
		mergeInto(next.Object, body)
		// The API server omits an emptied labels or annotations map.
		if m, _ := next.Object["metadata"].(map[string]any); m != nil {
			for _, f := range []string{"labels", "annotations"} {
				if inner, ok := m[f].(map[string]any); ok && len(inner) == 0 {
					delete(m, f)
				}
			}
		}
		next.SetResourceVersion(p.obj.GetResourceVersion())
		next.SetUID(p.obj.GetUID())
		if dry {
			return true, next, nil
		}
		p.obj = next
		p.bump()
		return true, p.obj.DeepCopy(), nil
	})
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "stable.example.com/v1", APIResources: []metav1.APIResource{
			{Name: "crontabs", Kind: "CronTab", Namespaced: true, Verbs: []string{"get", "list", "patch"}},
		}},
	}
	return kubesweep.NewWith(disc, dyn)
}

type optimisticLockErr struct{}

func (optimisticLockErr) Error() string {
	return "the object has been modified; please apply your changes to the latest version and try again"
}

var errOptimisticLock error = optimisticLockErr{}

func newPinCluster(labels map[string]string) *pinCluster {
	p := &pinCluster{obj: liveCrontabAnnotated(labels, map[string]string{markers.AddressAnnotation: "kubernetes_manifest.cron"}), rv: 41}
	p.obj.SetUID("uid-original")
	p.bump()
	return p
}

// liveMvToOtherEstate is what live-mv's label write does to the object:
// the estate label now names the other estate.
func liveMvToOtherEstate(p *pinCluster) {
	l := p.obj.GetLabels()
	l[markers.TagEstate] = otherEstate
	p.obj.SetLabels(l)
	p.bump()
}

func TestRelease_ManifestSurfacePatchIsPinnedToTheObjectItChecked(t *testing.T) {
	p := newPinCluster(map[string]string{"app": "cron", markers.TagEstate: "smoke-crd"})
	checkedRV, checkedUID := p.obj.GetResourceVersion(), string(p.obj.GetUID())
	p.beforeWrite = liveMvToOtherEstate

	res, _ := Release(context.Background(), manifestProvider(), p.client(t), testKey, []Target{crontabTarget()})

	if got := p.obj.GetLabels()[markers.TagEstate]; got != otherEstate {
		t.Errorf("%s = %q after the release, want %q: live-mv moved the object between the check and the patch, and the unpinned patch deleted the other estate's label", markers.TagEstate, got, otherEstate)
	}
	if out := res.Outcomes[0]; out.OK {
		t.Errorf("outcome = %s, want a failure: the release did not happen", out)
	}
	for i, body := range p.patches {
		meta, _ := body["metadata"].(map[string]any)
		if meta["resourceVersion"] != checkedRV || meta["uid"] != checkedUID {
			t.Errorf("patch %d carries resourceVersion=%v uid=%v, want %q and %q, the object the release checked: %v", i, meta["resourceVersion"], meta["uid"], checkedRV, checkedUID, body)
		}
	}
}

// The control: with no other writer in the window, the pinned patch lands.
// Without this, a fake server that refused every patch would pass the test
// above.
func TestRelease_ManifestSurfacePinnedPatchLandsWhenNothingMoved(t *testing.T) {
	p := newPinCluster(map[string]string{"app": "cron", markers.TagEstate: "smoke-crd"})
	res, diags := Release(context.Background(), manifestProvider(), p.client(t), testKey, []Target{crontabTarget()})
	if out := res.Outcomes[0]; !out.OK || diags.HasErrors() {
		t.Fatalf("outcome = %s (diags %v), want RELEASED", out, diags.Err())
	}
	if _, still := p.obj.GetLabels()[markers.TagEstate]; still {
		t.Fatalf("labels after release = %v, want %s gone", p.obj.GetLabels(), markers.TagEstate)
	}
}
