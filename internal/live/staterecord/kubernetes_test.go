// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes/fake"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

const fakeRecordNamespace = "tofu-records-test"

// fakeSecrets is a Secret client over client-go's fake clientset, with the
// namespace already created so a write is not refused for the namespace's
// absence.
func fakeSecrets(t *testing.T) corev1client.SecretInterface {
	t.Helper()
	cs := fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fakeRecordNamespace}})
	return cs.CoreV1().Secrets(fakeRecordNamespace)
}

func newFakeKubernetesStore(t *testing.T, estate string) *KubernetesStore {
	t.Helper()
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:   fakeSecrets(t),
		Namespace: fakeRecordNamespace,
		Estate:    estate,
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	return store
}

// TestKubernetesStoreRefusesAnEstateNameThatCannotBeALabelValue is the open-time
// half of decision 2 and #1396's sibling case. An estate name may be 128
// characters; a Kubernetes label value caps at 63. Every Secret this store
// writes carries tofu-estate, and that label is what estate-boundary.yaml
// fences a write with, so an estate whose name cannot be one has no fence at
// all - which is worth a refusal at open rather than a run whose records are
// unlabelled.
func TestKubernetesStoreRefusesAnEstateNameThatCannotBeALabelValue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		estate string
		want   string
	}{
		{"64 characters", strings.Repeat("e", 64), "cannot be a Kubernetes label value"},
		{"a slash", "team/prod", "cannot be a Kubernetes label value"},
		{"empty", "", "must not be empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewKubernetesStore(KubernetesConfig{
				Secrets:   fakeSecrets(t),
				Namespace: fakeRecordNamespace,
				Estate:    tc.estate,
			})
			if err == nil {
				t.Fatalf("NewKubernetesStore with estate %q: no error, want a refusal naming the estate", tc.estate)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("NewKubernetesStore err = %v, want it to contain %q", err, tc.want)
			}
		})
	}

	// The boundary case passes, so this cannot be a check that refuses
	// everything: 63 characters is exactly what a label value holds.
	if _, err := NewKubernetesStore(KubernetesConfig{
		Secrets:   fakeSecrets(t),
		Namespace: fakeRecordNamespace,
		Estate:    strings.Repeat("e", 63),
	}); err != nil {
		t.Errorf("NewKubernetesStore with a 63-character estate: %v, want it accepted", err)
	}
}

// TestKubernetesSecretNameIsAHashUnderAReadablePrefix is decision 1. The name
// is fixed-length whatever the key is, which is the whole reason it is a hash:
// a record key carries "/" and base64url runs and is routinely longer than the
// 253 characters a DNS-1123 subdomain holds.
func TestKubernetesSecretNameIsAHashUnderAReadablePrefix(t *testing.T) {
	store := newFakeKubernetesStore(t, "prod")
	short := store.SecretName("k1")
	long := store.SecretName("tofu-records/prod/aws_vpc/" + strings.Repeat("A", 2000))
	for _, name := range []string{short, long} {
		if !strings.HasPrefix(name, KubernetesSecretNamePrefix) {
			t.Errorf("SecretName = %q, want it to start with %q", name, KubernetesSecretNamePrefix)
		}
		if len(name) != len(KubernetesSecretNamePrefix)+64 {
			t.Errorf("SecretName = %q (%d characters), want the prefix plus a 64-character SHA-256", name, len(name))
		}
		if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
			t.Errorf("SecretName %q is not a valid Secret name: %v", name, errs)
		}
	}
	if short == long {
		t.Error("two different keys hashed to the same Secret name")
	}
}

// TestKubernetesLongestS3KeyRoundTripsThroughTheAnnotation checks decision 1's
// annotation size question against the longest key the other remote store
// accepts. An S3 object key is at most 1024 bytes; a Kubernetes object's
// annotations are capped at 256 KiB in total, so the key annotation has two
// orders of magnitude of room and the check is that the round trip is exact,
// not that it is near a limit.
func TestKubernetesLongestS3KeyRoundTripsThroughTheAnnotation(t *testing.T) {
	store := newFakeKubernetesStore(t, "prod")
	ctx := context.Background()
	const s3MaxKeyBytes = 1024
	head := "tofu-records/prod/aws_vpc/"
	key := head + strings.Repeat("A", s3MaxKeyBytes-len(head))
	if len(key) != s3MaxKeyBytes {
		t.Fatalf("the test key is %d bytes, want %d", len(key), s3MaxKeyBytes)
	}
	if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent on a 1024-byte key: %v", err)
	}
	keys, err := store.List(ctx, "tofu-records/prod/")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0] != key {
		t.Fatalf("List returned %v, want exactly the 1024-byte key back", keys)
	}
	payload, _, exists, err := store.Get(ctx, key)
	if err != nil || !exists || string(payload) != "v" {
		t.Errorf("Get on the 1024-byte key: payload=%q exists=%v err=%v", payload, exists, err)
	}
}

// TestKubernetesRefusesAnOversizedRecordByName is decision 4. The refusal is
// before the request and on the compressed length, so a record that compresses
// under the limit is written and one that does not is named.
func TestKubernetesRefusesAnOversizedRecordByName(t *testing.T) {
	store := newFakeKubernetesStore(t, "prod")
	ctx := context.Background()

	// Incompressible: random bytes gzip to slightly more than they came in as,
	// so this is genuinely over the limit after compression.
	big := make([]byte, MaxKubernetesRecordBytes+1<<16)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	_, err := store.PutIfAbsent(ctx, "tofu-records/prod/aws_thing/big", big)
	var tooLarge *RecordTooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("PutIfAbsent of an oversized record: got %v (%T), want *RecordTooLargeError", err, err)
	}
	if tooLarge.Limit != MaxKubernetesRecordBytes {
		t.Errorf("RecordTooLargeError.Limit = %d, want %d", tooLarge.Limit, MaxKubernetesRecordBytes)
	}
	if !strings.Contains(tooLarge.Error(), "tofu-records/prod/aws_thing/big") {
		t.Errorf("RecordTooLargeError.Error() = %q, want it to name the record", tooLarge.Error())
	}
	if _, _, exists, _ := store.Get(ctx, "tofu-records/prod/aws_thing/big"); exists {
		t.Error("the oversized record was written; the refusal must come before the request")
	}

	// The same number of bytes, compressible, is written: the limit is measured
	// after compression and not before, so this cannot pass by refusing
	// everything large.
	zeros := make([]byte, MaxKubernetesRecordBytes+1<<16)
	if _, err := store.PutIfAbsent(ctx, "tofu-records/prod/aws_thing/zeros", zeros); err != nil {
		t.Errorf("PutIfAbsent of a compressible record of the same length: %v, want it written", err)
	}
}

// TestKubernetesRefusesAHashCollisionByName is decision 1's last clause. The
// Secret name is a hash, so a Get has to check that the object it found holds
// the key that was asked for. The collision is manufactured by writing the
// annotation by hand, which is also the case that will really happen: an
// operator editing a record object.
func TestKubernetesRefusesAHashCollisionByName(t *testing.T) {
	secrets := fakeSecrets(t)
	store, err := NewKubernetesStore(KubernetesConfig{Secrets: secrets, Namespace: fakeRecordNamespace, Estate: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const key = "tofu-records/prod/aws_thing/one"
	if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	secret, err := secrets.Get(ctx, store.SecretName(key), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the Secret back: %v", err)
	}
	secret.Annotations[KubernetesRecordKeyAnnotation] = "tofu-records/prod/aws_thing/somethingelse"
	if _, err := secrets.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("editing the annotation: %v", err)
	}

	_, _, _, err = store.Get(ctx, key)
	var collision *KeyCollisionError
	if !errors.As(err, &collision) {
		t.Fatalf("Get over a colliding Secret: got %v (%T), want *KeyCollisionError", err, err)
	}
	if collision.FoundKey != "tofu-records/prod/aws_thing/somethingelse" {
		t.Errorf("KeyCollisionError.FoundKey = %q, want the key the object actually holds", collision.FoundKey)
	}

	// And List refuses rather than returning the key the object holds. It
	// used to return it, on the reasoning that a listing should say what the
	// object says; but the name is what a Get reads, and no Get of that key
	// reaches this object, so the listing was handing out a key nothing could
	// answer for. That is A3 in GitHub issue #1448, and the refusal names
	// both the Secret and the name the key hashes to.
	keys, err := store.List(ctx, "tofu-records/prod/")
	var misnamed *MisnamedRecordError
	if !errors.As(err, &misnamed) {
		t.Fatalf("List over the edited Secret: keys=%v err=%v (%T), want *MisnamedRecordError", keys, err, err)
	}
	if misnamed.SecretName != store.SecretName(key) {
		t.Errorf("the refusal names Secret %q, want %q", misnamed.SecretName, store.SecretName(key))
	}
	if misnamed.Key != "tofu-records/prod/aws_thing/somethingelse" {
		t.Errorf("the refusal names key %q, want the key the annotation holds", misnamed.Key)
	}
}

// TestKubernetesWritesTheEstateLabelAndTheAddressAnnotation is decision 3, the
// Kubernetes half of #1337. tofu-estate is a label because that is what
// estate-boundary.yaml reads; the address is an annotation because a label
// value caps at 63 characters and an address does not (#1016).
func TestKubernetesWritesTheEstateLabelAndTheAddressAnnotation(t *testing.T) {
	secrets := fakeSecrets(t)
	store, err := NewKubernetesStore(KubernetesConfig{Secrets: secrets, Namespace: fakeRecordNamespace, Estate: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	const addr = "module.a.module.b.aws_instance.this[\"a-very-long-instance-key-that-is-well-past-a-label-value\"]"
	ctx := WithObjectTags(context.Background(), map[string]string{
		"tofu-address": addr,
		// An estate tag from the context must never win over the store's own:
		// an object can only ever name the estate the store was opened for.
		"tofu-estate": "some-other-estate",
	})
	const key = "tofu-records/prod/aws_instance/one"
	if _, err := store.PutIfAbsent(ctx, key, []byte("v")); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}
	secret, err := secrets.Get(context.Background(), store.SecretName(key), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the Secret back: %v", err)
	}
	if got := secret.Labels["tofu-estate"]; got != "prod" {
		t.Errorf("tofu-estate label = %q, want %q (the store's own estate, never the context's)", got, "prod")
	}
	if got := secret.Annotations["tofu-address"]; got != addr {
		t.Errorf("tofu-address annotation = %q, want %q", got, addr)
	}
	if _, isLabel := secret.Labels["tofu-address"]; isLabel {
		t.Error("tofu-address was written as a LABEL; a label value caps at 63 characters and an address does not (#1016)")
	}
	if got := secret.Labels[KubernetesManagedByLabel]; got != KubernetesManagedByValue {
		t.Errorf("%s label = %q, want %q", KubernetesManagedByLabel, got, KubernetesManagedByValue)
	}
	if got := secret.Labels[KubernetesNamespaceLabel]; got != "tofu-records" {
		t.Errorf("%s label = %q, want %q", KubernetesNamespaceLabel, got, "tofu-records")
	}
	if got := secret.Annotations["encoding"]; got != "gzip" {
		t.Errorf("encoding annotation = %q, want %q", got, "gzip")
	}
}

// TestKubernetesListIgnoresForeignSecrets pins that a namespace holding
// something other than records still lists records only: the label selector
// keeps another estate's objects and non-record objects out, and an object
// carrying the labels but no key annotation is not invented a key for.
func TestKubernetesListIgnoresForeignSecrets(t *testing.T) {
	secrets := fakeSecrets(t)
	store, err := NewKubernetesStore(KubernetesConfig{Secrets: secrets, Namespace: fakeRecordNamespace, Estate: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewKubernetesStore(KubernetesConfig{Secrets: secrets, Namespace: fakeRecordNamespace, Estate: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.PutIfAbsent(ctx, "tofu-records/prod/aws_thing/mine", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if _, err := other.PutIfAbsent(ctx, "tofu-records/staging/aws_thing/theirs", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      "someone-elses-secret",
		Namespace: fakeRecordNamespace,
	}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      "labelled-but-not-a-record",
		Namespace: fakeRecordNamespace,
		Labels: map[string]string{
			KubernetesManagedByLabel: KubernetesManagedByValue,
			"tofu-estate":            "prod",
			KubernetesNamespaceLabel: "tofu-records",
		},
	}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}

	keys, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0] != "tofu-records/prod/aws_thing/mine" {
		t.Errorf("List = %v, want only this estate's one record", keys)
	}
}

// TestKubernetesGetAllIsOneListAndIsComplete pins [BulkReader]'s contract for
// the one backend that can really bulk-read: a LIST carries every Secret's
// data, so there is no per-key fetch to leave a hole in the map.
//
// Payloads and completeness only. client-go's fake clientset does not assign
// metadata.resourceVersion at all, so nothing about versions can be measured
// here and nothing here should be read as evidence about them. Every version
// assertion - that "" means absent, that an update moves it, that a stale one
// is refused - is in the conformance suite, which runs against a real API
// server (see kubernetes_conformance_test.go).
func TestKubernetesGetAllIsOneListAndIsComplete(t *testing.T) {
	store := newFakeKubernetesStore(t, "prod")
	ctx := context.Background()
	want := map[string]string{
		"tofu-records/prod/aws_thing/a": "payload-a",
		"tofu-records/prod/aws_thing/b": "payload-b",
		"tofu-records/prod/aws_thing/c": "payload-c",
	}
	for key, payload := range want {
		if _, err := store.PutIfAbsent(ctx, key, []byte(payload)); err != nil {
			t.Fatalf("PutIfAbsent(%q): %v", key, err)
		}
	}
	if _, err := store.PutIfAbsent(ctx, "tofu-outputs/prod/x", []byte("not in the prefix")); err != nil {
		t.Fatal(err)
	}
	all, err := store.GetAll(ctx, "tofu-records/prod/")
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != len(want) {
		t.Fatalf("GetAll returned %d records, want %d: %v", len(all), len(want), all)
	}
	for key, payload := range want {
		rec, ok := all[key]
		if !ok {
			t.Errorf("GetAll is missing %q", key)
			continue
		}
		if string(rec.Payload) != payload {
			t.Errorf("GetAll[%q].Payload = %q, want %q", key, rec.Payload, payload)
		}
	}
}
