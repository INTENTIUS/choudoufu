// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1104: `live-mv -from-estate` on a manifest-declared object
// is one merge patch of the tofu-estate label and the address annotation
// through the cluster's own API, under the run's credential and the
// block's field manager, sent first with dryRun=All. Before it, the move
// was refused by name with the equivalent `kubectl label` command.

// manifestMoveRequest is a cross-estate move of the manifest-declared
// object at labelTestType.database, from estate "app" to estate to, with
// the address unchanged.
func manifestMoveRequest(t *testing.T, cluster *manifestCluster, to string) Request {
	t.Helper()
	addr := mustAddr(t, labelTestType+".database")
	provider := newLabelTestCluster(t, manifestTestSchema(), cty.NullVal(manifestTestSchema().Block.ImpliedType()))
	req := labelTestRequest(t, provider, labelTestConfig(t, labelTestType, "database"), addr, addr, "app", to)
	req.Resolutions[0].ImportID = manifestTestImportID
	if cluster != nil {
		req.Clusters = cluster
	}
	return req
}

func TestMove_ManifestSurfaceMoveRewritesTheLabel(t *testing.T) {
	addr := labelTestType + ".database"
	cluster := &manifestCluster{object: liveCronTab(
		map[string]string{markers.TagEstate: "app", "team": "a"},
		map[string]string{"owner": "team-a", markers.AddressAnnotation: addr},
	)}
	res, diags := Move(t.Context(), manifestMoveRequest(t, cluster, "data"))
	if diags.HasErrors() {
		t.Fatalf("the move was refused: %s", diags.Err())
	}
	if res.Surface != markers.SurfaceManifest {
		t.Errorf("Surface = %q, want %q", res.Surface, markers.SurfaceManifest)
	}
	if res.NothingToWrite || !res.Written || !res.Verified {
		t.Errorf("NothingToWrite = %v, Written = %v, Verified = %v; want a verified write", res.NothingToWrite, res.Written, res.Verified)
	}
	if cluster.dryRuns != 1 || cluster.realRuns != 1 || cluster.labelled != 2 {
		t.Errorf("patches: %d dry, %d real, %d with a label; want one dry and one real, both setting the label", cluster.dryRuns, cluster.realRuns, cluster.labelled)
	}
	labels := cluster.object.GetLabels()
	if labels[markers.TagEstate] != "data" || labels["team"] != "a" || len(labels) != 2 {
		t.Errorf("live labels = %v, want tofu-estate=data beside the untouched team label", labels)
	}
	ann := cluster.object.GetAnnotations()
	if ann[markers.AddressAnnotation] != addr || ann["owner"] != "team-a" {
		t.Errorf("live annotations = %v, want owner and the address", ann)
	}
	if got := cluster.object.Object["spec"]; got.(map[string]any)["image"] != "cron" {
		t.Errorf("the spec moved: %v", got)
	}
	// labelTestConfig declares no field_manager, so the provider applies
	// the block under its default, and the patch must go there too.
	for _, m := range cluster.managers {
		if m != kubesweep.DefaultFieldManager {
			t.Errorf("a patch went under field manager %q, want %q", m, kubesweep.DefaultFieldManager)
		}
	}
	if res.LiveID != manifestTestImportID || res.Path != PathIdentity {
		t.Errorf("LiveID = %q, Path = %s", res.LiveID, res.Path)
	}
}

// TestMove_ManifestSurfaceMoveWritesTheAnnotationWhenAbsent: an object
// migrated before #1639 carries no address annotation; the move writes it
// with the label, as the metadata-block move does.
func TestMove_ManifestSurfaceMoveWritesTheAnnotationWhenAbsent(t *testing.T) {
	cluster := &manifestCluster{object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil)}
	if _, diags := Move(t.Context(), manifestMoveRequest(t, cluster, "data")); diags.HasErrors() {
		t.Fatalf("the move was refused: %s", diags.Err())
	}
	if got := cluster.object.GetAnnotations()[markers.AddressAnnotation]; got != labelTestType+".database" {
		t.Errorf("address annotation = %q after the move", got)
	}
	if got := cluster.object.GetLabels()[markers.TagEstate]; got != "data" {
		t.Errorf("tofu-estate = %q after the move", got)
	}
}

// TestMove_ManifestSurfaceMoveDryRunAsksTheServer: -dry-run of a move
// sends the patch with dryRun=All, so the server's admission verdict is
// read before anything is written, and stops there.
func TestMove_ManifestSurfaceMoveDryRunAsksTheServer(t *testing.T) {
	cluster := &manifestCluster{object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil)}
	req := manifestMoveRequest(t, cluster, "data")
	req.DryRun = true
	res, diags := Move(t.Context(), req)
	if diags.HasErrors() {
		t.Fatalf("the dry run was refused: %s", diags.Err())
	}
	if cluster.dryRuns != 1 || cluster.realRuns != 0 {
		t.Errorf("patches: %d dry, %d real; want exactly one dry run", cluster.dryRuns, cluster.realRuns)
	}
	if res.Written || res.Verified {
		t.Errorf("Written = %v, Verified = %v under -dry-run", res.Written, res.Verified)
	}
	if got := cluster.object.GetLabels()[markers.TagEstate]; got != "app" {
		t.Errorf("-dry-run moved the label to %q", got)
	}
}

// TestMove_ManifestSurfaceMoveAdmissionRefusal is the estate boundary in
// miniature: a policy that refuses a write setting tofu-estate=data
// answers the dry run, its words reach the caller, and nothing is sent
// for real. The control is the same move with the policy gone.
func TestMove_ManifestSurfaceMoveAdmissionRefusal(t *testing.T) {
	const denial = `admission webhook "estate-boundary" denied the request: principal may not write tofu-estate=data`
	policy := func(labels map[string]string) string {
		if labels[markers.TagEstate] == "data" {
			return denial
		}
		return ""
	}
	for _, dryRun := range []bool{false, true} {
		cluster := &manifestCluster{object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil), reject: policy}
		req := manifestMoveRequest(t, cluster, "data")
		req.DryRun = dryRun
		_, diags := Move(t.Context(), req)
		if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), denial) {
			t.Fatalf("dryRun=%v: diags = %v, want the policy's own refusal", dryRun, diags.Err())
		}
		if cluster.realRuns != 0 || cluster.rejected != 1 {
			t.Errorf("dryRun=%v: %d real patches, %d rejected; want the dry run rejected and nothing sent after it", dryRun, cluster.realRuns, cluster.rejected)
		}
		if got := cluster.object.GetLabels()[markers.TagEstate]; got != "app" {
			t.Errorf("dryRun=%v: a refused move left tofu-estate = %q", dryRun, got)
		}
	}

	control := &manifestCluster{object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil)}
	if _, diags := Move(t.Context(), manifestMoveRequest(t, control, "data")); diags.HasErrors() {
		t.Fatalf("with no policy the same move was refused, so the refusal above is the tool's: %s", diags.Err())
	}
	if got := control.object.GetLabels()[markers.TagEstate]; got != "data" {
		t.Errorf("control: tofu-estate = %q after the move", got)
	}
}

func TestMove_ManifestSurfaceMoveRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		object *unstructured.Unstructured
		mutate func(*unstructured.Unstructured)
		to     string
		noKube bool
		want   string
	}{
		"no cluster client":            {object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil), noKube: true, want: "No cluster client"},
		"no object at the natural key": {object: nil, want: "serves no object"},
		"an object in a third estate":  {object: liveCronTab(map[string]string{markers.TagEstate: "other"}, nil), want: "Live resource owned by another estate"},
		"an object already moved":      {object: liveCronTab(map[string]string{markers.TagEstate: "data"}, nil), want: "Live object already in this estate"},
		"an unlabelled object":         {object: liveCronTab(nil, nil), want: "carries no ownership label"},
		"an annotation naming a third address": {
			object: liveCronTab(map[string]string{markers.TagEstate: "app"}, map[string]string{markers.AddressAnnotation: "kubernetes_manifest.x"}),
			want:   "Live object carries another address",
		},
		"an estate that is not a label value": {
			object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil),
			to:     strings.Repeat("a", markers.LabelMaxValue+1),
			want:   "not a legal Kubernetes label value",
		},
		"a dry run that rewrites the spec": {
			object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil),
			mutate: func(u *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(u.Object, "tampered", "spec", "image")
			},
			want: "spec.image",
		},
		"a dry run that adds a label": {
			object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil),
			mutate: func(u *unstructured.Unstructured) {
				l := u.GetLabels()
				l["injected"] = "yes"
				u.SetLabels(l)
			},
			want: "metadata.labels",
		},
		"a dry run that sets the estate to something else": {
			object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil),
			mutate: func(u *unstructured.Unstructured) {
				u.SetLabels(map[string]string{markers.TagEstate: "rewritten"})
			},
			want: "metadata.labels",
		},
	} {
		t.Run(name, func(t *testing.T) {
			cluster := &manifestCluster{object: tc.object, mutate: tc.mutate}
			to := tc.to
			if to == "" {
				to = "data"
			}
			var req Request
			if tc.noKube {
				req = manifestMoveRequest(t, nil, to)
			} else {
				req = manifestMoveRequest(t, cluster, to)
			}
			_, diags := Move(t.Context(), req)
			if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), tc.want) {
				t.Fatalf("diags = %v, want an error saying %q", diags.Err(), tc.want)
			}
			if cluster.realRuns != 0 {
				t.Errorf("a refused move patched the object %d time(s)", cluster.realRuns)
			}
			if tc.object != nil && cluster.object.GetLabels()[markers.TagEstate] != tc.object.GetLabels()[markers.TagEstate] {
				t.Errorf("a refused move changed the live label to %q", cluster.object.GetLabels()[markers.TagEstate])
			}
		})
	}
}

// TestMove_ManifestSurfaceMoveUnderTheBlocksFieldManager runs the move
// against client-go's field-managed tracker (the API server's own
// managedfields code): the provider creates the object under the block's
// field manager, live-mv moves it, and the provider's next apply of the
// destination configuration - the label now reading the destination
// estate - meets no conflict, because the label went to that manager's
// Apply entry (GitHub issue #1704's hand-off, here for the label).
func TestMove_ManifestSurfaceMoveUnderTheBlocksFieldManager(t *testing.T) {
	for _, tc := range []struct{ declared, manager string }{
		{"my-pipeline", "my-pipeline"},
		{"", kubesweep.DefaultFieldManager},
	} {
		t.Run(tc.manager, func(t *testing.T) {
			clusters, dyn := newFieldManagedClusters(t)
			address := labelTestType + ".database_renamed"
			apply := func(estate string) (*unstructured.Unstructured, error) {
				ct := liveCronTab(map[string]string{markers.TagEstate: estate}, map[string]string{markers.AddressAnnotation: address})
				ct.SetResourceVersion("")
				return dyn.Resource(fieldManagerTestGVR).Namespace("smoke-crd").Apply(context.Background(), ct.GetName(), ct, metav1.ApplyOptions{FieldManager: tc.manager})
			}
			if _, err := apply("app"); err != nil {
				t.Fatalf("greenfield create under %q: %v", tc.manager, err)
			}

			addr := mustAddr(t, address)
			provider := newLabelTestCluster(t, manifestTestSchema(), cty.NullVal(manifestTestSchema().Block.ImpliedType()))
			req := labelTestRequest(t, provider, fieldManagerTestConfig(t, tc.declared), addr, addr, "app", "data")
			req.Resolutions[0].ImportID = manifestTestImportID
			req.Clusters = clusters
			res, diags := Move(t.Context(), req)
			if diags.HasErrors() {
				t.Fatalf("live-mv's move was refused: %s", diags.Err())
			}
			if !res.Written || !res.Verified {
				t.Fatalf("Written = %v, Verified = %v; want a verified write", res.Written, res.Verified)
			}

			after, err := apply("data")
			if err != nil {
				t.Fatalf("the provider's apply under %q after live-mv's move conflicted: %v", tc.manager, err)
			}
			if got := after.GetLabels()[markers.TagEstate]; got != "data" {
				t.Errorf("after the provider's apply tofu-estate = %q", got)
			}
			for _, e := range after.GetManagedFields() {
				if !strings.Contains(string(e.FieldsV1.Raw), "f:"+markers.TagEstate) {
					continue
				}
				if e.Manager != tc.manager || e.Operation != metav1.ManagedFieldsOperationApply {
					t.Errorf("%s/%s also owns the tofu-estate label: %s", e.Manager, e.Operation, e.FieldsV1.Raw)
				}
			}
		})
	}
}

// TestMove_ManifestSurfaceMovePatchNamesOnlyTheMarkers pins the request
// itself: the move's patch carries exactly the tofu-estate label and the
// address annotation, and nothing a merge patch could use to reach any
// other field.
func TestMove_ManifestSurfaceMovePatchNamesOnlyTheMarkers(t *testing.T) {
	rec := &recordingPatcher{manifestCluster: manifestCluster{object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil)}}
	req := manifestMoveRequest(t, nil, "data")
	req.Clusters = rec
	if _, diags := Move(t.Context(), req); diags.HasErrors() {
		t.Fatalf("the move was refused: %s", diags.Err())
	}
	if len(rec.labelKeys) != 2 || len(rec.annotationKeys) != 2 {
		t.Fatalf("recorded %d label sets and %d annotation sets, want one per patch", len(rec.labelKeys), len(rec.annotationKeys))
	}
	for i := range rec.labelKeys {
		if !slices.Equal(rec.labelKeys[i], []string{markers.TagEstate}) {
			t.Errorf("patch %d sets labels %v", i, rec.labelKeys[i])
		}
		if !slices.Equal(rec.annotationKeys[i], []string{markers.AddressAnnotation}) {
			t.Errorf("patch %d sets annotations %v", i, rec.annotationKeys[i])
		}
	}
}

// recordingPatcher is a manifestCluster that records the keys each patch
// names.
type recordingPatcher struct {
	manifestCluster
	labelKeys      [][]string
	annotationKeys [][]string
}

func (r *recordingPatcher) LabelPatcher(context.Context, addrs.AbsProviderConfig) (kubesweep.LabelPatcher, error) {
	return r, nil
}

func (r *recordingPatcher) PatchMarkers(ctx context.Context, ref kubesweep.ObjectRef, labels, annotations map[string]string, fieldManager string, dryRun bool) (*unstructured.Unstructured, string, error) {
	r.labelKeys = append(r.labelKeys, slices.Sorted(maps.Keys(labels)))
	r.annotationKeys = append(r.annotationKeys, slices.Sorted(maps.Keys(annotations)))
	return r.manifestCluster.PatchMarkers(ctx, ref, labels, annotations, fieldManager, dryRun)
}
