// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// TestKubernetesStoreLabelsWithTheMarker is where the two spellings of
// tofu-estate meet. internal/live/staterecord holds no choudoufu concepts and
// so writes the label as a literal; this fork reads it as markers.TagEstate,
// and live/kubernetes/estate-boundary.yaml fences on it. If the store's label
// ever stopped being the marker, every record Secret would be outside the
// fence and nothing else would fail.
func TestKubernetesStoreLabelsWithTheMarker(t *testing.T) {
	if staterecord.KubernetesEstateLabel != markers.TagEstate {
		t.Errorf("the Kubernetes record store writes %q and the marker is %q; estate-boundary.yaml fences on the marker, so a record Secret carrying anything else is outside the fence",
			staterecord.KubernetesEstateLabel, markers.TagEstate)
	}
}

// TestTheSweepKnowsTheRecordStoresOwnObjects pins the two strings
// internal/live/kubesweep tests a record Secret with against the store that
// writes them. Neither package imports the other, and this is where they meet.
//
// It is load-bearing and was measured failing before the exclusion existed: a
// record Secret carries the estate's tofu-estate label, the sweep reads that
// label as "in the estate", and an object in the estate that no block declares
// is an orphan the plan proposes to destroy. On kind on 2026-09-19 an
// ordinary second plan proposed destroying all five of the estate's own
// record Secrets.
func TestTheSweepKnowsTheRecordStoresOwnObjects(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"labels": map[string]any{
				staterecord.KubernetesManagedByLabel: staterecord.KubernetesManagedByValue,
				staterecord.KubernetesEstateLabel:    "prod",
			},
			"annotations": map[string]any{
				staterecord.KubernetesRecordKeyAnnotation: "tofu-records/prod/aws_thing/a",
			},
		},
	}}
	if !kubesweep.RecordStoreObject(obj) {
		t.Fatalf("the sweep does not recognise a record Secret the store wrote; an ordinary plan would propose destroying it as an orphan of the estate")
	}

	// Neither half alone: a user's own Secret carrying one of the two is not
	// this store's, and excluding it would hide a real orphan.
	onlyLabel := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"labels": map[string]any{
			staterecord.KubernetesManagedByLabel: staterecord.KubernetesManagedByValue,
		}},
	}}
	if kubesweep.RecordStoreObject(onlyLabel) {
		t.Error("an object with the managed-by label and no record-key annotation is excluded from the sweep")
	}
	onlyAnnotation := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"annotations": map[string]any{
			staterecord.KubernetesRecordKeyAnnotation: "tofu-records/prod/aws_thing/a",
		}},
	}}
	if kubesweep.RecordStoreObject(onlyAnnotation) {
		t.Error("an object with the record-key annotation and no managed-by label is excluded from the sweep")
	}
}

// TestKubernetesRecordNamespaceIsPerEstate is decision 2's default: a
// namespace derived from the estate name, so the isolating arrangement is the
// one an operator gets without asking. Two estates must never share one.
func TestKubernetesRecordNamespaceIsPerEstate(t *testing.T) {
	if a, b := KubernetesRecordNamespace("prod"), KubernetesRecordNamespace("staging"); a == b {
		t.Fatalf("two estates derive the same records namespace %q", a)
	}
	if got := KubernetesRecordNamespace("prod"); got != "tofu-records-prod" {
		t.Errorf("KubernetesRecordNamespace(\"prod\") = %q, want %q", got, "tofu-records-prod")
	}
}

// TestKubernetesNamespaceForRefusesADerivedNameTheClusterWillNotTake is the
// #1396 case from the namespace's side. An estate name may be 128 characters;
// a namespace is a DNS-1123 label capped at 63. The refusal names the
// argument that settles it rather than letting the API server answer with a
// generic Invalid on the first write.
func TestKubernetesNamespaceForRefusesADerivedNameTheClusterWillNotTake(t *testing.T) {
	long := strings.Repeat("e", 60)
	_, err := kubernetesNamespaceFor(&configs.LiveRecordStore{Type: "kubernetes"}, long)
	if err == nil {
		t.Fatalf("a %d-character estate derived an accepted namespace", len(long))
	}
	for _, want := range []string{"namespace", KubernetesRecordNamespace(long)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not contain %q: %v", want, err)
		}
	}

	// An explicit namespace settles it, which is what the refusal says to do.
	got, err := kubernetesNamespaceFor(&configs.LiveRecordStore{Type: "kubernetes", Namespace: "team-records", NamespaceSet: true}, long)
	if err != nil {
		t.Fatalf("an explicit namespace was still refused: %v", err)
	}
	if got != "team-records" {
		t.Errorf("namespace = %q, want the one the block named", got)
	}

	// A short estate derives one, so this cannot be a check that refuses
	// everything.
	if _, err := kubernetesNamespaceFor(&configs.LiveRecordStore{Type: "kubernetes"}, "prod"); err != nil {
		t.Errorf("estate \"prod\" was refused a derived namespace: %v", err)
	}
}

// TestKubernetesAttrsCarriesTheWholeConnectionBlock pins that every connection
// argument the record_store block decodes reaches the loader. A silently
// dropped one is a run that connects to the wrong cluster, or to none, with no
// diagnostic naming the argument that was ignored.
func TestKubernetesAttrsCarriesTheWholeConnectionBlock(t *testing.T) {
	rs := &configs.LiveRecordStore{
		Type: "kubernetes",
		Kubernetes: configs.LiveRecordStoreKubernetes{
			Host:                  "https://cluster.example:6443",
			Token:                 "a-token",
			Insecure:              true,
			InCluster:             true,
			ConfigPath:            "/a/kubeconfig",
			ConfigPaths:           []string{"/b", "/c"},
			ConfigContext:         "ctx",
			ConfigContextAuthInfo: "auth",
			ConfigContextCluster:  "cluster",
			ClientCertificate:     "cert-pem",
			ClientKey:             "key-pem",
			ClusterCACertificate:  "ca-pem",
			Exec: &configs.LiveRecordStoreExec{
				APIVersion: "client.authentication.k8s.io/v1beta1",
				Command:    "aws",
				Args:       []string{"eks", "get-token"},
				Env:        map[string]string{"AWS_PROFILE": "p"},
			},
		},
	}
	got := kubernetesAttrs(rs)
	want := kubesweep.Attrs{
		Host:                  "https://cluster.example:6443",
		Token:                 "a-token",
		Insecure:              true,
		InCluster:             true,
		ConfigPath:            "/a/kubeconfig",
		ConfigPaths:           []string{"/b", "/c"},
		ConfigContext:         "ctx",
		ConfigContextAuthInfo: "auth",
		ConfigContextCluster:  "cluster",
		ClientCertificate:     "cert-pem",
		ClientKey:             "key-pem",
		ClusterCACertificate:  "ca-pem",
		Exec: &kubesweep.ExecCredential{
			APIVersion: "client.authentication.k8s.io/v1beta1",
			Command:    "aws",
			Args:       []string{"eks", "get-token"},
			Env:        map[string]string{"AWS_PROFILE": "p"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attrs = %+v, want %+v", got, want)
	}
}
