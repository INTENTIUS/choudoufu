// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// These are claim 44's Kubernetes cell at unit scale (live/smoke/claims.json,
// "Reading another estate is declared"): a read of another estate's outputs
// through record_store "kubernetes" happens only when the record_store block
// declares it, goes to the other estate's namespace and nowhere else, can
// only read, and a refusal by the cluster's RBAC names the Role that grants
// it. live/smoke/drafts/k8s-an-estate-reads-another-by-declaring-it.sh is
// the same claim against a real API server.

// k8sCluster is one fake cluster holding the given namespaces.
func k8sCluster(namespaces ...string) *fake.Clientset {
	objs := make([]runtime.Object, 0, len(namespaces))
	for _, ns := range namespaces {
		objs = append(objs, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	return fake.NewClientset(objs...)
}

// k8sEstateStore is estate's own record store in namespace, the way
// newKubernetesStore opens it.
func k8sEstateStore(t *testing.T, cs *fake.Clientset, namespace, estate string) staterecord.Store {
	t.Helper()
	s, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(namespace),
		Clientset: cs,
		Namespace: namespace,
		KeyPrefix: backendKeyPrefix,
		Estate:    estate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore(%s, %s): %v", namespace, estate, err)
	}
	return s
}

// producerApplies records network's root outputs in namespace, through the
// producer's own writer, so the reader cannot drift from what is written.
func producerApplies(t *testing.T, cs *fake.Clientset, namespace string) {
	t.Helper()
	state := states.NewState()
	root := state.EnsureModule(nil)
	root.SetOutputValue("cluster_services_namespace", cty.StringVal("cluster-services"), false, "")
	root.SetOutputValue("token", cty.StringVal("s3cr3t"), true, "")
	WriteRootOutputValues(t.Context(), NewRootOutputStore(k8sEstateStore(t, cs, namespace, "network"), "network"), state)
	if _, _, exists, err := k8sEstateStore(t, cs, namespace, "network").Get(t.Context(), RootOutputKey("network", "cluster_services_namespace")); err != nil || !exists {
		t.Fatalf("the producer's output was not recorded in %s: exists=%v err=%v", namespace, exists, err)
	}
}

// k8sSource is app's reading side over cs, with the record_store block's
// declarations as decoded config would carry them.
func k8sSource(t *testing.T, cs *fake.Clientset, ownNamespace string, reads ...configs.LiveRecordStoreOutputRead) EstateOutputsSource {
	t.Helper()
	rs := &configs.LiveRecordStore{Type: "kubernetes", OutputReadsDeclared: true, Namespace: ownNamespace, NamespaceSet: true, ReadsOutputsOf: reads}
	src := NewEstateOutputsSource(k8sEstateStore(t, cs, ownNamespace, "app"), rs, "app", "")
	// The production opener builds a client from the block's connection
	// arguments; this one hands the same fake cluster to the same store.
	src.OpenDeclared = func(_ context.Context, other, namespace string) (staterecord.Store, error) {
		return newDeclaredKubernetesReadStore(cs, other, namespace)
	}
	return *src
}

// secretActionsIn is every action the cluster saw on Secrets in namespace,
// as "verb name".
func secretActionsIn(cs *fake.Clientset, namespace string) []string {
	var out []string
	for _, a := range cs.Actions() {
		if a.GetResource().Resource != "secrets" || a.GetNamespace() != namespace {
			continue
		}
		out = append(out, a.GetVerb())
	}
	return out
}

// TestKubernetesUndeclaredReadIsRefusedBeforeAnythingIsSent is the default
// isolation: with no reads_outputs_of block, a read of another estate's
// outputs is refused by name and the cluster is never asked, neither in the
// other estate's namespace nor in this estate's own (where it could only
// have answered "not recorded").
func TestKubernetesUndeclaredReadIsRefusedBeforeAnythingIsSent(t *testing.T) {
	cs := k8sCluster("tofu-records-app", "tofu-records-network")
	producerApplies(t, cs, "tofu-records-network")
	cs.ClearActions()

	src := k8sSource(t, cs, "tofu-records-app")
	values, diags := ReadEstateOutputs(t.Context(), src, "network", []string{"cluster_services_namespace"})
	if values != nil {
		t.Errorf("an undeclared read returned values: %v", values)
	}
	desc := onlyError(t, diags)
	if desc.Summary != SummaryEstateOutputsUndeclared {
		t.Fatalf("summary = %q, want %q\n%s", desc.Summary, SummaryEstateOutputsUndeclared, desc.Detail)
	}
	for _, want := range []string{`estate "network"`, `reads_outputs_of "network" {}`, `"tofu-records-network"`} {
		if !strings.Contains(desc.Detail, want) {
			t.Errorf("detail does not say %q:\n%s", want, desc.Detail)
		}
	}
	if got := cs.Actions(); len(got) != 0 {
		t.Errorf("an undeclared read sent %d request(s) to the cluster: %v", len(got), got)
	}
}

// TestKubernetesUndeclaredInASharedNamespaceIsStillRefused: two estates
// whose records share one namespace by configuration are no different. The
// producer's record is reachable by name from the reader's own store, and
// the read is still refused for want of the declaration.
func TestKubernetesUndeclaredInASharedNamespaceIsStillRefused(t *testing.T) {
	cs := k8sCluster("platform-records")
	producerApplies(t, cs, "platform-records")
	cs.ClearActions()

	src := k8sSource(t, cs, "platform-records", configs.LiveRecordStoreOutputRead{Estate: "dns"})
	_, diags := ReadEstateOutputs(t.Context(), src, "network", []string{"cluster_services_namespace"})
	if desc := onlyError(t, diags); desc.Summary != SummaryEstateOutputsUndeclared {
		t.Fatalf("summary = %q, want %q", desc.Summary, SummaryEstateOutputsUndeclared)
	}
	if got := cs.Actions(); len(got) != 0 {
		t.Errorf("an undeclared read sent %d request(s) to the cluster: %v", len(got), got)
	}
}

// TestKubernetesDeclaredReadReadsTheOtherNamespaceOnly is the claim: a
// declared read plans with the producer's value, says how old it is, and
// touches the other estate's namespace with get and nothing else.
func TestKubernetesDeclaredReadReadsTheOtherNamespaceOnly(t *testing.T) {
	cs := k8sCluster("tofu-records-app", "tofu-records-network")
	producerApplies(t, cs, "tofu-records-network")
	cs.ClearActions()

	src := k8sSource(t, cs, "tofu-records-app", configs.LiveRecordStoreOutputRead{Estate: "network"})
	values, diags := ReadEstateOutputs(t.Context(), src, "network", []string{"cluster_services_namespace"})
	if diags.HasErrors() {
		t.Fatalf("a declared read failed: %s", diags.Err())
	}
	if got := values["cluster_services_namespace"]; !got.RawEquals(cty.StringVal("cluster-services")) {
		t.Errorf("cluster_services_namespace = %#v", got)
	}
	if len(diags) != 1 || diags[0].Severity() != tfdiags.Warning || diags[0].Description().Summary != SummaryEstateOutputsAsOf {
		t.Errorf("want the as-of warning alone, got %v", diags)
	}
	got := secretActionsIn(cs, "tofu-records-network")
	if len(got) == 0 {
		t.Fatal("the read sent nothing to tofu-records-network, so the value came from somewhere else")
	}
	for _, verb := range got {
		if verb != "get" {
			t.Errorf("a declared read sent %q to the other estate's namespace; it may only get: %v", verb, got)
		}
	}
	if own := secretActionsIn(cs, "tofu-records-app"); len(own) != 0 {
		t.Errorf("a declared read of network went to this estate's own namespace: %v", own)
	}

	// A sensitive output is never recorded, so it never crosses, here as on
	// every other backend.
	_, diags = ReadEstateOutputs(t.Context(), src, "network", []string{"token"})
	desc := onlyError(t, diags)
	if desc.Summary != SummaryEstateOutputNotRecorded || !strings.Contains(desc.Detail, `namespace "tofu-records-network"`) {
		t.Errorf("a sensitive output: got %q: %s", desc.Summary, desc.Detail)
	}
}

// TestKubernetesDeclaredReadInASharedNamespace: the namespace argument
// points the read at a namespace both estates share, and the record
// labelled as network's is served because the read store is opened as
// network. The reader's own store would refuse the same Secret as foreign
// (#1355), which is what made the read impossible before.
func TestKubernetesDeclaredReadInASharedNamespace(t *testing.T) {
	cs := k8sCluster("platform-records")
	producerApplies(t, cs, "platform-records")

	own := k8sEstateStore(t, cs, "platform-records", "app")
	_, _, _, err := own.Get(t.Context(), RootOutputKey("network", "cluster_services_namespace"))
	var foreign *staterecord.ForeignEstateRecordError
	if !errors.As(err, &foreign) {
		t.Fatalf("the reader's own store answered %v for network's record; this test assumes it refuses it as foreign", err)
	}

	src := k8sSource(t, cs, "platform-records", configs.LiveRecordStoreOutputRead{Estate: "network", Namespace: "platform-records", NamespaceSet: true})
	values, diags := ReadEstateOutputs(t.Context(), src, "network", []string{"cluster_services_namespace"})
	if diags.HasErrors() {
		t.Fatalf("a declared read in a shared namespace failed: %s", diags.Err())
	}
	if got := values["cluster_services_namespace"]; !got.RawEquals(cty.StringVal("cluster-services")) {
		t.Errorf("cluster_services_namespace = %#v", got)
	}
}

// TestKubernetesDeclaredReadDeniedNamesTheGrant is the BREAK arm at unit
// scale: the read is declared and the cluster's RBAC refuses the get. The
// plan must refuse by name, naming estate network, the namespace, and the
// Role that grants exactly this read, and never answer "not recorded".
func TestKubernetesDeclaredReadDeniedNamesTheGrant(t *testing.T) {
	cs := k8sCluster("tofu-records-app", "tofu-records-network")
	producerApplies(t, cs, "tofu-records-network")
	cs.PrependReactor("get", "secrets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetNamespace() != "tofu-records-network" {
			return false, nil, nil
		}
		name := a.(k8stesting.GetAction).GetName()
		return true, nil, k8serrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, name,
			errors.New(`User "system:serviceaccount:default:app" cannot get resource "secrets" in API group "" in the namespace "tofu-records-network"`))
	})

	src := k8sSource(t, cs, "tofu-records-app", configs.LiveRecordStoreOutputRead{Estate: "network"})
	values, diags := ReadEstateOutputs(t.Context(), src, "network", []string{"cluster_services_namespace"})
	if values != nil {
		t.Errorf("a denied read returned values: %v", values)
	}
	desc := onlyError(t, diags)
	if desc.Summary != SummaryEstateOutputsDenied {
		t.Fatalf("summary = %q, want %q\n%s", desc.Summary, SummaryEstateOutputsDenied, desc.Detail)
	}
	secret := staterecord.KubernetesSecretName(RootOutputKey("network", "cluster_services_namespace"))
	for _, want := range []string{
		`reading estate "network"'s output "cluster_services_namespace"`,
		`in namespace "tofu-records-network"`,
		"kubectl -n tofu-records-network create role tofu-reads-outputs-of-network --verb=get --resource=secrets --resource-name=" + secret,
		"create rolebinding tofu-reads-outputs-of-network-app",
		"cannot get resource",
	} {
		if !strings.Contains(desc.Detail, want) {
			t.Errorf("detail does not say %q:\n%s", want, desc.Detail)
		}
	}
	if strings.Contains(desc.Detail, "--verb=get,list") || strings.Contains(desc.Detail, "render-policy.sh") {
		t.Errorf("the remedy grants more than get, or names the s3 renderer:\n%s", desc.Detail)
	}
}

// TestKubernetesDeclaredReadOfAMissingNamespace: a declared namespace that
// is not there is a refusal naming the reads_outputs_of block, and not the
// store's own remedy, which would tell the reader to create the other
// estate's namespace and grant itself every verb in it.
func TestKubernetesDeclaredReadOfAMissingNamespace(t *testing.T) {
	cs := k8sCluster("tofu-records-app")
	src := k8sSource(t, cs, "tofu-records-app", configs.LiveRecordStoreOutputRead{Estate: "network"})
	_, diags := ReadEstateOutputs(t.Context(), src, "network", []string{"cluster_services_namespace"})
	desc := onlyError(t, diags)
	if desc.Summary != SummaryEstateOutputsUnreadable {
		t.Fatalf("summary = %q, want %q\n%s", desc.Summary, SummaryEstateOutputsUnreadable, desc.Detail)
	}
	for _, want := range []string{`reads_outputs_of "network"`, `"tofu-records-network"`, "does not exist"} {
		if !strings.Contains(desc.Detail, want) {
			t.Errorf("detail does not say %q:\n%s", want, desc.Detail)
		}
	}
	if strings.Contains(desc.Detail, "\n\n  kubectl create namespace") || strings.HasPrefix(desc.Detail, "Create") {
		t.Errorf("the refusal tells the reader to create the other estate's namespace:\n%s", desc.Detail)
	}
}

// TestDeclaredReadStoreOnlyReads: whatever the identity may do, the store a
// declared read goes through refuses every write, delete and listing, and
// sends none of them.
func TestDeclaredReadStoreOnlyReads(t *testing.T) {
	cs := k8sCluster("tofu-records-network")
	s, err := newDeclaredKubernetesReadStore(cs, "network", "tofu-records-network")
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	key := RootOutputKey("network", "x")
	if _, err := s.PutIfAbsent(ctx, key, []byte("{}")); !errors.Is(err, errReadOnlyStore) {
		t.Errorf("PutIfAbsent: %v", err)
	}
	if _, err := s.PutIfVersion(ctx, key, []byte("{}"), "1"); !errors.Is(err, errReadOnlyStore) {
		t.Errorf("PutIfVersion: %v", err)
	}
	if err := s.Delete(ctx, key, "1"); !errors.Is(err, errReadOnlyStore) {
		t.Errorf("Delete: %v", err)
	}
	if _, err := s.List(ctx, RootOutputKeyPrefix("network")); !errors.Is(err, errReadOnlyStore) {
		t.Errorf("List: %v", err)
	}
	if got := secretActionsIn(cs, "tofu-records-network"); len(got) != 0 {
		t.Errorf("a refused write still reached the cluster: %v", got)
	}
}

// TestNewEstateOutputsSourceDeclarations: the default namespace is the
// other estate's own default, an explicit one wins, and only a kubernetes
// store carries declarations at all.
func TestNewEstateOutputsSourceDeclarations(t *testing.T) {
	rs := &configs.LiveRecordStore{Type: "kubernetes", OutputReadsDeclared: true, ReadsOutputsOf: []configs.LiveRecordStoreOutputRead{
		{Estate: "network"},
		{Estate: "dns", Namespace: "platform-records", NamespaceSet: true},
	}}
	src := NewEstateOutputsSource(nil, rs, "app", "down")
	if got := src.Declared["network"]; got != "tofu-records-network" {
		t.Errorf("network reads from %q, want tofu-records-network", got)
	}
	if got := src.Declared["dns"]; got != "platform-records" {
		t.Errorf("dns reads from %q, want platform-records", got)
	}
	if src.OpenDeclared == nil || src.StoreType != "kubernetes" || src.Unavailable != "down" {
		t.Errorf("source = %+v", src)
	}

	s3 := NewEstateOutputsSource(nil, &configs.LiveRecordStore{Type: "s3", Bucket: "records"}, "app", "")
	if s3.Declared != nil || s3.OpenDeclared != nil || s3.Bucket != "records" {
		t.Errorf("an s3 source carries Kubernetes declarations: %+v", s3)
	}
	if none := NewEstateOutputsSource(nil, nil, "app", "no store"); none.StoreType != "" || none.Unavailable != "no store" {
		t.Errorf("a source with no record_store block = %+v", none)
	}
}

// TestDeclaredReadOfAnOverlongDefaultNamespace: a default namespace past 63
// characters is refused naming the argument that settles it.
func TestDeclaredReadOfAnOverlongDefaultNamespace(t *testing.T) {
	long := "n" + strings.Repeat("e", 55)
	d := &declaredKubernetesReads{rs: &configs.LiveRecordStore{Type: "kubernetes"}}
	_, err := d.open(t.Context(), long, KubernetesRecordNamespace(long))
	if err == nil || !strings.Contains(err.Error(), "namespace argument") {
		t.Errorf("open = %v, want a refusal naming the namespace argument", err)
	}
}
