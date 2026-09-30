// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/managedfields"
	fakediscovery "k8s.io/client-go/discovery/fake"
	fakedynamic "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1720: live-mv's manifest rename wrote the address
// annotation under kubesweep.DefaultFieldManager whatever the block's
// `field_manager { name = ... }` said, so [kubesweep.Client.PatchMarkers]
// handed the annotation to "Terraform"'s Apply entry, and the provider's
// next apply under the block's own manager - the next rename - reported
// a field manager conflict with "Terraform". These run the rename through
// Move against client-go's field-managed tracker, which runs the API
// server's own managedfields code, so a conflict here is the conflict a
// cluster reports.

var fieldManagerTestGVR = schema.GroupVersionResource{Group: "stable.example.com", Version: "v1", Resource: "crontabs"}

// fieldManagedClusters is [Clusters] over a real [kubesweep.Client] whose
// dynamic client goes through a field-managed tracker.
type fieldManagedClusters struct {
	client *kubesweep.Client
}

func (c fieldManagedClusters) LabelPatcher(context.Context, addrs.AbsProviderConfig) (kubesweep.LabelPatcher, error) {
	return c.client, nil
}

func newFieldManagedClusters(t *testing.T) (fieldManagedClusters, *fakedynamic.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	gv := schema.GroupVersion{Group: "stable.example.com", Version: "v1"}
	scheme.AddKnownTypeWithName(gv.WithKind("CronTab"), &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(gv.WithKind("CronTabList"), &unstructured.UnstructuredList{})
	tracker := clienttesting.NewFieldManagedObjectTracker(scheme, serializer.NewCodecFactory(scheme).UniversalDecoder(), managedfields.NewDeducedTypeConverter())
	dyn := fakedynamic.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{fieldManagerTestGVR: "CronTabList"})
	dyn.ReactionChain = nil
	dyn.AddReactor("*", "*", clienttesting.ObjectReaction(tracker))
	disc := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	disc.Resources = []*metav1.APIResourceList{
		{GroupVersion: "stable.example.com/v1", APIResources: []metav1.APIResource{
			{Name: "crontabs", Kind: "CronTab", Namespaced: true, Verbs: []string{"get", "list", "patch"}},
		}},
	}
	return fieldManagedClusters{client: kubesweep.NewWith(disc, dyn)}, dyn
}

// fieldManagerProviderApply is hashicorp/kubernetes applying a
// kubernetes_manifest block: the whole manifest, markers stamped in, as a
// server-side apply under the block's field manager, force_conflicts
// unset.
func fieldManagerProviderApply(dyn *fakedynamic.FakeDynamicClient, manager, address string) (*unstructured.Unstructured, error) {
	ct := liveCronTab(map[string]string{markers.TagEstate: "app"}, map[string]string{markers.AddressAnnotation: address})
	ct.SetResourceVersion("")
	return dyn.Resource(fieldManagerTestGVR).Namespace("smoke-crd").Apply(context.Background(), ct.GetName(), ct, metav1.ApplyOptions{FieldManager: manager})
}

// fieldManagerTestConfig declares the renamed block, with a field_manager
// block naming manager when manager is not empty.
func fieldManagerTestConfig(t *testing.T, manager string) *configs.Config {
	t.Helper()
	fm := ""
	if manager != "" {
		fm = `
  field_manager {
    name = "` + manager + `"
  }`
	}
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
terraform {
  required_providers {
    kubernetes = {
      source = "hashicorp/kubernetes"
    }
  }
}

resource "`+labelTestType+`" "database_renamed" {
  provider = kubernetes
  metadata {
    name      = "database"
    namespace = "boundary"
  }`+fm+`
}
`)
	return loadConfigDir(t, dir)
}

// renameThenProviderRename walks the sequence: the provider creates the
// object under manager, live-mv renames database to database_renamed, and
// the provider's next apply under manager renames it again.
func renameThenProviderRename(t *testing.T, declared, manager string) *unstructured.Unstructured {
	t.Helper()
	clusters, dyn := newFieldManagedClusters(t)
	if _, err := fieldManagerProviderApply(dyn, manager, labelTestType+".database"); err != nil {
		t.Fatalf("greenfield create under %q: %v", manager, err)
	}

	old := mustAddr(t, labelTestType+".database")
	renamed := mustAddr(t, labelTestType+".database_renamed")
	provider := newLabelTestCluster(t, manifestTestSchema(), cty.NullVal(manifestTestSchema().Block.ImpliedType()))
	req := labelTestRequest(t, provider, fieldManagerTestConfig(t, declared), old, renamed, "", "app")
	req.Resolutions[0].ImportID = manifestTestImportID
	req.Clusters = clusters
	res, diags := Move(t.Context(), req)
	if diags.HasErrors() {
		t.Fatalf("live-mv's rename was refused: %s", diags.Err())
	}
	if !res.Written || !res.Verified {
		t.Fatalf("Written = %v, Verified = %v; want a verified write", res.Written, res.Verified)
	}

	after, err := fieldManagerProviderApply(dyn, manager, labelTestType+".database_third")
	if err != nil {
		t.Fatalf("the provider's rename under %q after live-mv's conflicted (#1720): %v", manager, err)
	}
	if got := after.GetAnnotations()[markers.AddressAnnotation]; got != labelTestType+".database_third" {
		t.Errorf("after the provider's rename the annotation is %q", got)
	}
	return after
}

// assertAnnotationOwnedBy checks that the address annotation belongs to
// manager's Apply entry and to no other entry.
func assertAnnotationOwnedBy(t *testing.T, obj *unstructured.Unstructured, manager string) {
	t.Helper()
	owned := false
	for _, e := range obj.GetManagedFields() {
		if !strings.Contains(string(e.FieldsV1.Raw), "f:"+markers.AddressAnnotation) {
			continue
		}
		if e.Manager == manager && e.Operation == metav1.ManagedFieldsOperationApply {
			owned = true
			continue
		}
		t.Errorf("%s/%s also owns the address annotation: %s", e.Manager, e.Operation, e.FieldsV1.Raw)
	}
	if !owned {
		t.Errorf("%s/Apply does not own the address annotation", manager)
	}
}

func TestMove_ManifestRenameUnderTheBlocksFieldManager(t *testing.T) {
	after := renameThenProviderRename(t, "my-pipeline", "my-pipeline")
	assertAnnotationOwnedBy(t, after, "my-pipeline")
}

// TestMove_ManifestRenameWithoutFieldManagerUsesTheDefault is the control:
// a block that declares no field_manager is applied by the provider under
// "Terraform", and live-mv's write must go there too.
func TestMove_ManifestRenameWithoutFieldManagerUsesTheDefault(t *testing.T) {
	after := renameThenProviderRename(t, "", kubesweep.DefaultFieldManager)
	assertAnnotationOwnedBy(t, after, kubesweep.DefaultFieldManager)
}

// TestMove_ManifestRenameUnderAVariableFieldManager reads the name the way
// every other identity-bearing argument is read, through the static
// evaluator, so a name from a variable's default is honoured too.
func TestMove_ManifestRenameUnderAVariableFieldManager(t *testing.T) {
	clusters, dyn := newFieldManagedClusters(t)
	if _, err := fieldManagerProviderApply(dyn, "from-var", labelTestType+".database"); err != nil {
		t.Fatal(err)
	}
	req := fieldManagerRequest(t, clusters, `var.manager`, `
variable "manager" {
  default = "from-var"
}
`)
	if _, diags := Move(t.Context(), req); diags.HasErrors() {
		t.Fatalf("live-mv's rename was refused: %s", diags.Err())
	}
	after, err := fieldManagerProviderApply(dyn, "from-var", labelTestType+".database_third")
	if err != nil {
		t.Fatalf("the provider's rename after live-mv's conflicted: %v", err)
	}
	assertAnnotationOwnedBy(t, after, "from-var")
}

// TestMove_ManifestRenameRefusesAnUnresolvableFieldManager: a name this
// run cannot resolve is refused before anything is sent, rather than
// guessed as "Terraform" and left to conflict at the next apply.
func TestMove_ManifestRenameRefusesAnUnresolvableFieldManager(t *testing.T) {
	cluster := &manifestCluster{object: liveCronTab(map[string]string{markers.TagEstate: "app"}, nil)}
	req := fieldManagerRequest(t, cluster, `var.manager`, `
variable "manager" {
  type = string
}
`)
	_, diags := Move(t.Context(), req)
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), "Cannot tell the block's field manager") {
		t.Fatalf("diags = %v, want the field manager refusal", diags.Err())
	}
	if cluster.dryRuns+cluster.realRuns != 0 {
		t.Errorf("a refused rename sent %d patch(es)", cluster.dryRuns+cluster.realRuns)
	}
}

// fieldManagerRequest is the rename request with the destination block's
// field_manager name set to the expression nameExpr, and extra appended
// to the configuration.
func fieldManagerRequest(t *testing.T, clusters Clusters, nameExpr, extra string) Request {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
terraform {
  required_providers {
    kubernetes = {
      source = "hashicorp/kubernetes"
    }
  }
}
`+extra+`
resource "`+labelTestType+`" "database_renamed" {
  provider = kubernetes
  metadata {
    name      = "database"
    namespace = "boundary"
  }
  field_manager {
    name = `+nameExpr+`
  }
}
`)
	old := mustAddr(t, labelTestType+".database")
	renamed := mustAddr(t, labelTestType+".database_renamed")
	provider := newLabelTestCluster(t, manifestTestSchema(), cty.NullVal(manifestTestSchema().Block.ImpliedType()))
	req := labelTestRequest(t, provider, loadConfigDir(t, dir), old, renamed, "", "app")
	req.Resolutions[0].ImportID = manifestTestImportID
	req.Clusters = clusters
	return req
}
