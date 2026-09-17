// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"sort"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// lbl1211Entries are the managedFields kind v1.36.1's API server held for
// the ConfigMap GitHub issue #1211 reproduced against, read with kubectl
// get --raw after one choudoufu apply plus one `kubectl label` and pasted
// verbatim. This is the external source these tests consult: mutate it and
// they stop agreeing with what an API server actually writes.
//
// Three managers, which is the whole point. "Terraform" is the provider's
// own server-side apply and owns the keys the configuration declared.
// "kubectl-label" owns one label nothing in the configuration names.
// "kube-apiserver" is here for the Namespace case the naive widening
// tripped over.
var lbl1211Entries = []metav1.ManagedFieldsEntry{
	{
		Manager:   "Terraform",
		Operation: metav1.ManagedFieldsOperationApply,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:data":{"f:k":{}},"f:metadata":{"f:annotations":{"f:owner":{},"f:reviewed":{}},"f:labels":{"f:squad":{},"f:tier":{},"f:tofu-estate":{}}}}`)},
	},
	{
		Manager:   "kubectl-label",
		Operation: metav1.ManagedFieldsOperationUpdate,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{"f:external":{}}}}`)},
	},
	{
		Manager:   "kube-apiserver",
		Operation: metav1.ManagedFieldsOperationUpdate,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{".":{},"f:kubernetes.io/metadata.name":{}}}}`)},
	},
}

func lbl1211Object(entries ...metav1.ManagedFieldsEntry) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("ConfigMap")
	u.SetNamespace("lbl1211")
	u.SetName("cm")
	u.SetManagedFields(entries)
	return u
}

func sortedKeys(set map[string]bool) string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

var metadataMaps = []string{"labels", "annotations"}

// TestManagedMetadataKeys is GitHub issue #1211's exactness requirement:
// the answer is OUR manager's keys, not every key on the object. The
// wholesale alternative - mirroring the live maps - makes the removal plan
// and then proposes deleting the other two managers' keys for ever,
// measured on a real cluster and recorded on the issue.
func TestManagedMetadataKeys(t *testing.T) {
	keys, present := ManagedMetadataKeys(lbl1211Object(lbl1211Entries...), "Terraform", metadataMaps)
	if !present {
		t.Fatal("an object carrying three managedFields entries reported none")
	}
	if got, want := sortedKeys(keys["labels"]), "squad,tier,tofu-estate"; got != want {
		t.Errorf("labels = %q, want %q", got, want)
	}
	if got, want := sortedKeys(keys["annotations"]), "owner,reviewed"; got != want {
		t.Errorf("annotations = %q, want %q", got, want)
	}
	// The two keys the churn came from, named individually so a failure
	// says which invariant broke.
	if keys["labels"]["external"] {
		t.Error("a label kubectl wrote was reported as ours; mirroring it proposes a deletion the server undoes")
	}
	if keys["labels"]["kubernetes.io/metadata.name"] {
		t.Error("the API server's own label was reported as ours; mirroring it is the churn #1211's scouting measured")
	}
	if keys["labels"]["."] || keys["annotations"]["."] {
		t.Error(`the set marker "." was read as a key`)
	}
}

// TestManagedMetadataKeysHonoursTheFieldManagerName: managedFields is keyed
// by manager NAME. A block that sets `field_manager { name = ... }` writes
// under that name, and asking about the default would find no entry and
// report - confidently, and wrongly - that this estate owns nothing.
func TestManagedMetadataKeysHonoursTheFieldManagerName(t *testing.T) {
	renamed := []metav1.ManagedFieldsEntry{{
		Manager:   "my-pipeline",
		Operation: metav1.ManagedFieldsOperationApply,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{"f:squad":{}}}}`)},
	}}
	obj := lbl1211Object(renamed...)

	keys, present := ManagedMetadataKeys(obj, "my-pipeline", metadataMaps)
	if !present || !keys["labels"]["squad"] {
		t.Fatalf("the declared field manager's own key was not found: %v", keys)
	}
	keys, present = ManagedMetadataKeys(obj, "", metadataMaps)
	if !present {
		t.Fatal("managedFields were present and reported absent")
	}
	if len(keys["labels"]) != 0 {
		t.Errorf("the default manager was credited with another manager's keys: %v", keys["labels"])
	}
	if keys["annotations"] == nil {
		t.Error("a named metadata map came back absent rather than empty")
	}
}

// TestManagedMetadataKeysDistinguishesEmptyFromUnknowable is the
// degradation contract. "Our manager owns nothing here" is an exact
// answer worth acting on; "this object carries no managedFields at all" is
// not an answer, and a caller that cannot tell them apart is back to
// planning No changes over a key it should have removed.
func TestManagedMetadataKeysDistinguishesEmptyFromUnknowable(t *testing.T) {
	// Present, ours owns nothing: an object some other tool created that
	// this estate has adopted but never applied.
	keys, present := ManagedMetadataKeys(lbl1211Object(lbl1211Entries[1]), "Terraform", metadataMaps)
	if !present {
		t.Error("an object with one foreign managedFields entry reported none")
	}
	if len(keys["labels"]) != 0 || len(keys["annotations"]) != 0 {
		t.Errorf("keys = %v, want empty sets", keys)
	}

	// Absent: nothing can be concluded.
	if _, present := ManagedMetadataKeys(lbl1211Object(), "Terraform", metadataMaps); present {
		t.Error("an object with no managedFields reported that it had some")
	}
	if _, present := ManagedMetadataKeys(nil, "Terraform", metadataMaps); present {
		t.Error("a nil object reported managedFields")
	}
}

// TestManagedMetadataKeysSkipsSubresourcesAndUnionsOperations: a status
// writer is not a writer of the object's own metadata, and a manager that
// has both applied and updated the object wrote everything both entries
// name.
func TestManagedMetadataKeysSkipsSubresourcesAndUnionsOperations(t *testing.T) {
	entries := []metav1.ManagedFieldsEntry{
		{
			Manager:   "Terraform",
			Operation: metav1.ManagedFieldsOperationApply,
			FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{"f:squad":{}}}}`)},
		},
		{
			Manager:   "Terraform",
			Operation: metav1.ManagedFieldsOperationUpdate,
			FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{"f:tier":{}}}}`)},
		},
		{
			Manager:     "Terraform",
			Operation:   metav1.ManagedFieldsOperationUpdate,
			Subresource: "status",
			FieldsV1:    &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{"f:statusonly":{}}}}`)},
		},
	}
	keys, _ := ManagedMetadataKeys(lbl1211Object(entries...), "Terraform", metadataMaps)
	if got, want := sortedKeys(keys["labels"]), "squad,tier"; got != want {
		t.Errorf("labels = %q, want %q", got, want)
	}
}

// TestManagedMetadataKeysReadsDottedAndSlashedKeys: a label key holds dots
// and slashes, so "f:" is the only thing that may be read off a member's
// spelling. A parser that split on "." or "/" would lose these.
func TestManagedMetadataKeysReadsDottedAndSlashedKeys(t *testing.T) {
	entries := []metav1.ManagedFieldsEntry{{
		Manager:   "Terraform",
		Operation: metav1.ManagedFieldsOperationApply,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{"f:app.kubernetes.io/name":{},"f:app.kubernetes.io/part-of":{}}}}`)},
	}}
	keys, _ := ManagedMetadataKeys(lbl1211Object(entries...), "Terraform", metadataMaps)
	if got, want := sortedKeys(keys["labels"]), "app.kubernetes.io/name,app.kubernetes.io/part-of"; got != want {
		t.Errorf("labels = %q, want %q", got, want)
	}
}
