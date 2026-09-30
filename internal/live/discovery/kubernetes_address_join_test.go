// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1737: the Kubernetes leg's address join decoded the
// annotation and compared addr.String(), so two annotations that name a
// declared instance never bound it - a for_each key made of digits, which
// decodes as a count index, and an address a moved block retired - and in
// both the object was filed as an orphan while #1617's refusal lifted,
// because the sweep's account of the instance came back present and empty.
// The plan destroyed the object and created one at its name.

// digitKeyInstance is kubernetes_config_map_v1.reader[key], a for_each
// instance.
func digitKeyInstance(key string) addrs.AbsResourceInstance {
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_config_map_v1", Name: "reader"}.Instance(addrs.StringKey(key)).Absolute(addrs.RootModuleInstance)
}

// joinRequest is a node-refused block (#1539's shape: its name reads a
// value the static evaluator cannot follow) and one listed ConfigMap
// carrying annotation.
func joinRequest(refused []addrs.AbsResourceInstance, annotation string, cfg *configs.Config) Request {
	nodeRefused := map[string]bool{}
	for _, r := range refused {
		nodeRefused[r.String()] = true
	}
	return Request{
		Estate: "m1116",
		Sweepers: []Sweeper{KubernetesSweep{Client: &stubSweeper{
			kinds:   []kubesweep.Kind{configMapKind()},
			objects: map[string][]kubesweep.Object{"ConfigMap": {labelled("ConfigMap", "m1116-res", "reader-0", annotation)}},
		}, Types: k8sTypes(), ManifestType: "kubernetes_manifest"}},
		NodeRefused: nodeRefused,
		Config:      cfg,
	}
}

// assertBoundNotOrphaned is what the annotation promises: the object is
// the instance's, bound at its address, and not proposed for removal; and
// the instance is out of the unaddressed account because the marker index
// answers for it.
func assertBoundNotOrphaned(t *testing.T, res *Result, want addrs.AbsResourceInstance) {
	t.Helper()
	if len(res.Orphans) != 0 {
		t.Errorf("orphans = %v; the ConfigMap's annotation names %s", res.Orphans, want)
	}
	b, ok := res.BindingFor(want)
	if !ok || b.ImportID != "m1116-res/reader-0" {
		t.Errorf("binding for %s = %+v (present %v); want m1116-res/reader-0", want, b, ok)
	}
	r, ok := resolutionAt(res, want.String())
	if !ok || r.ImportID != "m1116-res/reader-0" {
		t.Errorf("resolution at %s = %+v (present %v)", want, r, ok)
	}
	if objects, present := res.KubernetesUnaddressed[want.String()]; present {
		t.Errorf("KubernetesUnaddressed[%s] = %q (present); a bound instance is answered by the marker index", want, objects)
	}
}

// TestKubernetesSweepBindsADigitForEachKey: for_each = toset(["0", "1"]).
// reader["0"] stamps as kubernetes_config_map_v1.reader:0, and the object
// carrying that is reader["0"]'s, not an orphan of a count instance
// reader[0] nothing declares.
func TestKubernetesSweepBindsADigitForEachKey(t *testing.T) {
	zero, one := digitKeyInstance("0"), digitKeyInstance("1")
	annotation := markers.EscapeAddress(zero.String())
	if annotation != "kubernetes_config_map_v1.reader:0" {
		t.Fatalf("EscapeAddress(%s) = %q; this test's premise is the digit key's stamp", zero, annotation)
	}
	req := joinRequest([]addrs.AbsResourceInstance{zero, one}, annotation, nil)
	res := &Result{}
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	assertBoundNotOrphaned(t, res, zero)

	// reader["1"] has no object: nothing listed could be its, so the
	// create is safe.
	got, present := res.KubernetesUnaddressed[one.String()]
	if !present || len(got) != 0 {
		t.Errorf("KubernetesUnaddressed[%s] = %q (present %v), want present and empty", one, got, present)
	}
}

// movedJoinConfig renames kubernetes_config_map_v1.old to reader with a
// moved block, and nothing ran live-mv: the object still carries the old
// address.
func movedJoinConfig(t *testing.T) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	src := `
resource "kubernetes_config_map_v1" "reader" {
  metadata {
    name      = "reader-${var.v}"
    namespace = "m1116-res"
  }
}
variable "v" { default = "0" }
moved {
  from = kubernetes_config_map_v1.old
  to   = kubernetes_config_map_v1.reader
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return loadConfig(t, dir)
}

// TestKubernetesSweepFollowsAMovedBlock is the AWS legs' moved aliasing
// (GitHub issue #198) on the Kubernetes leg: an object annotated with the
// address a moved block retired is the new address's object.
func TestKubernetesSweepFollowsAMovedBlock(t *testing.T) {
	reader := k8sInstance(t, "kubernetes_config_map_v1", "reader")
	req := joinRequest([]addrs.AbsResourceInstance{reader}, "kubernetes_config_map_v1.old", movedJoinConfig(t))
	res := &Result{}
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	assertBoundNotOrphaned(t, res, reader)
}

// Without the moved block the old address is a deleted block's, and its
// object is the orphan it always was. This is the other half of the moved
// test: the alias comes from the statement, not from any leniency in the
// comparison.
func TestKubernetesSweepWithoutTheMovedBlockLeavesTheOrphan(t *testing.T) {
	reader := k8sInstance(t, "kubernetes_config_map_v1", "reader")
	req := joinRequest([]addrs.AbsResourceInstance{reader}, "kubernetes_config_map_v1.old", nil)
	res := &Result{}
	if diags := sweepKubernetes(context.Background(), req, res); diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if len(res.Orphans) != 1 || res.Orphans[0].ImportID != "m1116-res/reader-0" {
		t.Fatalf("orphans = %v, want the ConfigMap annotated for a block nothing declares", res.Orphans)
	}
	if _, ok := res.BindingFor(reader); ok {
		t.Fatalf("bound %s to an object annotated for kubernetes_config_map_v1.old with no moved block", reader)
	}
}

// TestUnboundAnnotationForTheInstanceKeepsTheRefusal is the refusal
// boundary: an object whose annotation names the refused instance, and
// which the join did not settle, could be that instance's object - it is
// the one thing most likely to be. It must be counted, so the node's
// refusal stands, rather than read as "annotated for some other block"
// and lift it. The join is bypassed here (settled is empty) so the test
// holds whatever the join's reason for not binding: a parse, an alias, or
// a rule added later.
func TestUnboundAnnotationForTheInstanceKeepsTheRefusal(t *testing.T) {
	zero := digitKeyInstance("0")
	reader := k8sInstance(t, "kubernetes_config_map_v1", "reader")
	leg := KubernetesSweep{Types: k8sTypes(), ManifestType: "kubernetes_manifest"}

	for _, tc := range []struct {
		name       string
		refused    addrs.AbsResourceInstance
		annotation string
		cfg        *configs.Config
		want       []string
	}{
		{"a digit for_each key", zero, markers.EscapeAddress(zero.String()), nil, []string{"ConfigMap m1116-res/reader-0"}},
		{"the instance's own address", reader, markers.EscapeAddress(reader.String()), nil, []string{"ConfigMap m1116-res/reader-0"}},
		{"an address a moved block retired", reader, "kubernetes_config_map_v1.old", movedJoinConfig(t), []string{"ConfigMap m1116-res/reader-0"}},
		// Unchanged from #1641: an object annotated for a block nothing
		// declares is a deleted block's orphan, and not this block's.
		{"an address nothing declares", reader, "kubernetes_config_map_v1.old", nil, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{Estate: "m1116", NodeRefused: map[string]bool{tc.refused.String(): true}, Config: tc.cfg}
			undeclared := []UndeclaredObject{{Kind: configMapKind(), TypeName: "kubernetes_config_map_v1", Object: labelled("ConfigMap", "m1116-res", "reader-0", tc.annotation)}}
			res := &Result{}
			accountUnaddressed(req, leg, nil, undeclared, map[int]bool{}, res)
			got, present := res.KubernetesUnaddressed[tc.refused.String()]
			if !present || !reflect.DeepEqual(nonNil(got), tc.want) {
				t.Fatalf("KubernetesUnaddressed[%s] = %q (present %v), want %q", tc.refused, got, present, tc.want)
			}
			if len(tc.want) == 0 {
				return
			}

			var refusal tfdiags.Diagnostics
			refusal = refusal.Append(tfdiags.Sourceless(tfdiags.Error, "Identity not resolvable from configuration", "reader.name refers to kubernetes_manifest.crontab.object.spec.image"))
			node := &projection.NodeResolver{
				MarkerIndex:        projection.NewMarkerIndex(nil),
				StaticRefusals:     map[string]tfdiags.Diagnostics{tc.refused.String(): refusal},
				UnaddressedObjects: res.KubernetesUnaddressed,
			}
			_, found, diags := node.ResolveResourceIdentity(context.Background(), tc.refused, readerValue(), configMapSchema())
			refused := false
			for _, d := range diags {
				if d.Severity() == tfdiags.Error && d.Description().Summary == projection.SummaryIdentityUnresolvedNoAddress {
					refused = true
				}
			}
			if found || !refused {
				t.Fatalf("found=%v refused=%v; an object annotated for %s that did not bind must keep #1617's refusal. diags: %v", found, refused, tc.refused, diags.ErrWithWarnings())
			}
		})
	}
}
