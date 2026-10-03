// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// GitHub issue #1524. These drive encryption_at_rest on a managed control
// plane through a fake provider. What they measure is the judgement: which
// provider answers become which outcome, and that an answer about some other
// cluster is never believed. What the real providers say is
// internal/live/managedk8s's to read and live/managed-k8s/harness.sh's to
// measure against a real cluster.

type fakeControlPlane struct {
	got   ControlPlaneEncryption
	err   error
	asked []ManagedControlPlane
}

func (f *fakeControlPlane) SecretsEncryption(_ context.Context, cp ManagedControlPlane) (ControlPlaneEncryption, error) {
	f.asked = append(f.asked, cp)
	return f.got, f.err
}

const eksHost = "https://ABCDEF0123456789.gr7.us-east-1.eks.amazonaws.com"

func eksPlane() *ManagedControlPlane {
	return &ManagedControlPlane{Provider: ControlPlaneEKS, Name: "prod", Region: "us-east-1", Source: "test"}
}

func managedCheck(t *testing.T, reader ControlPlaneReader, host string) Finding {
	t.Helper()
	cs := withReviews(fake.NewClientset(), func(ns, verb string) bool { return true })
	// A managed control plane's encryption is never read off a Pod: if the
	// check lists kube-system on this path it has fallen back to the kind
	// reading, and that is a failure of this test, not a NOT CHECKED.
	cs.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		t.Fatalf("encryption_at_rest listed Pods on a named managed control plane")
		return true, nil, nil
	})
	findings := check(t, cs, ClusterContractOptions{
		NamespaceKnownToExist: true,
		ControlPlane:          eksPlane(),
		ControlPlaneReader:    reader,
		APIServerHost:         host,
	})
	return findingFor(t, findings, ClusterEncryptionAtRest)
}

func TestManagedEncryptionOnIsAPass(t *testing.T) {
	r := &fakeControlPlane{got: ControlPlaneEncryption{
		Endpoints: []string{eksHost},
		Verdict:   EncryptionOn,
		Detail:    "encryptionConfig envelope-encrypts secrets with KMS key arn:aws:kms:us-east-1:111122223333:key/k",
	}}
	f := managedCheck(t, r, eksHost+":443")
	if f.Outcome != Passed {
		t.Fatalf("outcome %v, want Passed: %s", f.Outcome, f.Found)
	}
	if !strings.Contains(f.Found, "arn:aws:kms:us-east-1:111122223333:key/k") || !strings.Contains(f.Found, `EKS cluster "prod"`) {
		t.Errorf("the finding does not name the cluster and the key it read: %s", f.Found)
	}
	if len(r.asked) != 1 || r.asked[0].Name != "prod" {
		t.Errorf("the provider was asked %v, want the one named control plane", r.asked)
	}
}

func TestManagedEncryptionOffFails(t *testing.T) {
	f := managedCheck(t, &fakeControlPlane{got: ControlPlaneEncryption{
		Endpoints: []string{eksHost},
		Verdict:   EncryptionOff,
		Detail:    "encryptionConfig names no KMS key for secrets",
	}}, eksHost)
	if f.Outcome != Failed {
		t.Fatalf("outcome %v, want Failed: %s", f.Outcome, f.Found)
	}
	if !strings.Contains(f.Found, "encryptionConfig names no KMS key") {
		t.Errorf("the failure does not carry the provider's reason: %s", f.Found)
	}
}

func TestManagedEncryptionUndeterminedIsNotChecked(t *testing.T) {
	f := managedCheck(t, &fakeControlPlane{got: ControlPlaneEncryption{
		Endpoints: []string{eksHost},
		Detail:    "databaseEncryption.currentState is CURRENT_STATE_ENCRYPTION_PENDING",
	}}, eksHost)
	if f.Outcome != NotChecked {
		t.Fatalf("outcome %v, want NotChecked: %s", f.Outcome, f.Found)
	}
}

// TestManagedEncryptionAboutAnotherClusterIsNotBelieved is the reason the
// endpoint check exists. A control_plane block naming the wrong cluster, or
// an inferred name that is not this cluster's, must not turn a green read
// off some other cluster into a pass for this one - and it must not be a
// FAIL either, since nothing about THIS cluster was learned.
func TestManagedEncryptionAboutAnotherClusterIsNotBelieved(t *testing.T) {
	for _, verdict := range []EncryptionVerdict{EncryptionOn, EncryptionOff} {
		f := managedCheck(t, &fakeControlPlane{got: ControlPlaneEncryption{
			Endpoints: []string{"https://OTHER.gr7.us-east-1.eks.amazonaws.com"},
			Verdict:   verdict,
			Detail:    "whatever the other cluster says",
		}}, eksHost)
		if f.Outcome != NotChecked {
			t.Fatalf("verdict %v about another cluster came out %v, want NotChecked: %s", verdict, f.Outcome, f.Found)
		}
		if !strings.Contains(f.Found, "OTHER.gr7") || !strings.Contains(f.Found, "ABCDEF0123456789") {
			t.Errorf("the finding does not name both addresses: %s", f.Found)
		}
	}
}

func TestManagedEncryptionWithNoHostIsNotBelieved(t *testing.T) {
	f := managedCheck(t, &fakeControlPlane{got: ControlPlaneEncryption{
		Endpoints: []string{eksHost}, Verdict: EncryptionOn, Detail: "on",
	}}, "")
	if f.Outcome != NotChecked {
		t.Fatalf("outcome %v with no host to compare, want NotChecked: %s", f.Outcome, f.Found)
	}
}

func TestManagedEncryptionDeniedIsNotChecked(t *testing.T) {
	f := managedCheck(t, &fakeControlPlane{err: &ControlPlaneDeniedError{
		Action: "eks:DescribeCluster", Err: errors.New("User: arn:aws:iam::1:user/x is not authorized"),
	}}, eksHost)
	if f.Outcome != NotChecked {
		t.Fatalf("outcome %v, want NotChecked: %s", f.Outcome, f.Found)
	}
	if !strings.Contains(f.Found, "eks:DescribeCluster") || !strings.Contains(f.Found, "not readable from here") {
		t.Errorf("the finding does not name the permission it lacked: %s", f.Found)
	}
}

func TestManagedEncryptionMissingClusterIsNotChecked(t *testing.T) {
	f := managedCheck(t, &fakeControlPlane{err: &ControlPlaneNotFoundError{Err: errors.New("No cluster found for name: prod.")}}, eksHost)
	if f.Outcome != NotChecked {
		t.Fatalf("outcome %v, want NotChecked: %s", f.Outcome, f.Found)
	}
	if !strings.Contains(f.Found, "control_plane block") {
		t.Errorf("the finding does not point at the block that named it: %s", f.Found)
	}
}

func TestManagedEncryptionOtherErrorIsNotChecked(t *testing.T) {
	f := managedCheck(t, &fakeControlPlane{err: errors.New("dial tcp: no route to host")}, eksHost)
	if f.Outcome != NotChecked {
		t.Fatalf("outcome %v, want NotChecked: %s", f.Outcome, f.Found)
	}
}

func TestManagedEncryptionWithNoReaderIsNotChecked(t *testing.T) {
	f := managedCheck(t, nil, eksHost)
	if f.Outcome != NotChecked {
		t.Fatalf("outcome %v, want NotChecked: %s", f.Outcome, f.Found)
	}
}

// TestNoStaticPodPointsAtTheControlPlaneBlock: with no control plane named,
// the kind reading stays, and its NOT CHECKED now says how to get an answer.
func TestNoStaticPodPointsAtTheControlPlaneBlock(t *testing.T) {
	cs := withReviews(fake.NewClientset(), func(ns, verb string) bool { return true })
	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEncryptionAtRest)
	if f.Outcome != NotChecked {
		t.Fatalf("outcome %v, want NotChecked: %s", f.Outcome, f.Found)
	}
	if !strings.Contains(f.Found, "control_plane block") {
		t.Errorf("the NOT CHECKED does not say how to get an answer: %s", f.Found)
	}
}

func TestKubernetesStoreCarriesTheControlPlaneToTheContract(t *testing.T) {
	cs := withReviews(fake.NewClientset(), func(ns, verb string) bool { return true })
	r := &fakeControlPlane{got: ControlPlaneEncryption{Endpoints: []string{eksHost}, Verdict: EncryptionOn, Detail: "on"}}
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:            cs.CoreV1().Secrets(contractNamespace),
		Clientset:          cs,
		Namespace:          contractNamespace,
		Estate:             "alice",
		ControlPlane:       eksPlane(),
		ControlPlaneReader: r,
		APIServerHost:      eksHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := store.CheckContract(context.Background(), ContractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f := findingFor(t, findings, ClusterEncryptionAtRest); f.Outcome != Passed {
		t.Fatalf("outcome %v through the store, want Passed: %s", f.Outcome, f.Found)
	}
	if len(r.asked) != 1 {
		t.Fatalf("the provider was asked %d times through the store, want 1", len(r.asked))
	}
}

func TestEndpointMatches(t *testing.T) {
	for _, c := range []struct {
		host      string
		endpoints []string
		want      bool
	}{
		{"https://abc.gr7.us-east-1.eks.amazonaws.com", []string{"https://ABC.gr7.us-east-1.eks.amazonaws.com"}, true},
		{"https://abc.gr7.us-east-1.eks.amazonaws.com:443", []string{"https://abc.gr7.us-east-1.eks.amazonaws.com"}, true},
		{"https://34.1.2.3", []string{"34.1.2.3"}, true},
		{"https://prod-dns-1.hcp.westeurope.azmk8s.io:443", []string{"prod-dns-1.hcp.westeurope.azmk8s.io"}, true},
		{"https://34.1.2.3:6443", []string{"34.1.2.3"}, false},
		{"https://34.1.2.3", []string{"34.1.2.4"}, false},
		{"", []string{"34.1.2.3"}, false},
		{"https://34.1.2.3", nil, false},
	} {
		if got := endpointMatches(c.host, c.endpoints); got != c.want {
			t.Errorf("endpointMatches(%q, %v) = %v, want %v", c.host, c.endpoints, got, c.want)
		}
	}
}
