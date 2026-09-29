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
// get --raw after one choudoufu apply plus one `kubectl label
// external=keepme` and pasted verbatim. This is the external source these
// tests consult: mutate it and they stop agreeing with what an API server
// actually writes.
//
// Two managers. "Terraform" is the provider's own server-side apply and,
// at this point in the sequence, owns exactly the keys the configuration
// declared. "kubectl-label" owns one label nothing in the configuration
// names. See launderedEntries below for what the SAME object's entries
// look like one apply later, which is not the same thing at all.
//
// nsLabelsWithNoOwner records the other half of the same reading: the
// Namespace this estate declared carried labels
// {kubernetes.io/metadata.name, tofu-estate} and a single managedFields
// entry, Terraform's, naming only f:tofu-estate. The API server writes
// kubernetes.io/metadata.name with NO managedFields entry of its own, so
// at this point no manager owns it and nothing can mistake it for ours.
var lbl1211Entries = []metav1.ManagedFieldsEntry{
	{
		Manager:   "Terraform",
		Operation: metav1.ManagedFieldsOperationApply,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:data":{"f:greeting":{}},"f:metadata":{"f:annotations":{"f:owner":{},"f:reviewed":{}},"f:labels":{"f:squad":{},"f:tier":{},"f:tofu-estate":{}}}}`)},
	},
	{
		Manager:   "kubectl-label",
		Operation: metav1.ManagedFieldsOperationUpdate,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{"f:external":{}}}}`)},
	},
}

// nsBeforeLaundering is that Namespace's one entry, verbatim.
var nsBeforeLaundering = []metav1.ManagedFieldsEntry{{
	Manager:   "Terraform",
	Operation: metav1.ManagedFieldsOperationApply,
	FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{"f:tofu-estate":{}}}}`)},
}}

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
	// The key the churn came from, named on its own so a failure says
	// which invariant broke.
	if keys["labels"]["external"] {
		t.Error("a label kubectl wrote was reported as ours; mirroring it proposes a deletion the server undoes")
	}
	// And the Namespace's own label, which at this point has no owner at
	// all - the API server writes it outside the field-manager mechanism.
	ns, present := ManagedMetadataKeys(lbl1211Object(nsBeforeLaundering...), "Terraform", metadataMaps)
	if !present {
		t.Fatal("the namespace fixture reported no managedFields")
	}
	if got, want := sortedKeys(ns["labels"]), "tofu-estate"; got != want {
		t.Errorf("namespace labels = %q, want %q - kubernetes.io/metadata.name has no manager here", got, want)
	}
}

// TestManagedMetadataKeysSkipsTheSetMarker: a FieldsV1 set spells its own
// presence as "." beside its "f:<key>" members, and reading that as a key
// would put a label named "." in every removal set.
func TestManagedMetadataKeysSkipsTheSetMarker(t *testing.T) {
	entries := []metav1.ManagedFieldsEntry{{
		Manager:   "Terraform",
		Operation: metav1.ManagedFieldsOperationApply,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{".":{},"f:app":{}}}}`)},
	}}
	keys, _ := ManagedMetadataKeys(lbl1211Object(entries...), "Terraform", metadataMaps)
	if got, want := sortedKeys(keys["labels"]), "app"; got != want {
		t.Errorf("labels = %q, want %q", got, want)
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

// launderedEntries are the managedFields kind v1.36.1 held after the
// sequence GitHub issue #1211's smoke claim runs: apply, `kubectl label
// owner=...`, `kubectl annotate scraped=...`, then one more apply that
// changed the object but left one of the two metadata maps alone. Read
// with kubectl get --raw and pasted verbatim.
//
// They are here because they REFUTE the premise this file was written on.
// hashicorp/kubernetes's computed_fields rule makes PlanResourceChange
// take the LIVE value of metadata.labels and metadata.annotations whenever
// the configuration equals the prior manifest - so the apply SENDS every
// key the object already had, including keys the configuration has never
// named, and server-side apply records our field manager as their writer.
// One apply later, managedFields says we own them.
//
// The Namespace case is the one that settles it: after a second apply,
// "Terraform" owns f:kubernetes.io/metadata.name, which the API server
// writes on every Namespace and no configuration can declare away, and NO
// OTHER MANAGER owns it - so no co-ownership filter can exclude it either.
// A removal rule built on this set proposes deleting it, the server writes
// it straight back, and the plan churns for ever: exactly the failure
// #1211's scouting measured for the wholesale mirror, arriving one apply
// later.
//
// So managedFields answers "who wrote this field" exactly, and that is not
// the same question as "did this configuration declare it". The removal
// set comes from the estate's own record of what the configuration last
// declared instead (internal/live/projection's
// residueFields.ManifestMetadataKeys); what this function produces is kept
// as the rail that narrows it.
var launderedEntries = struct {
	namespace []metav1.ManagedFieldsEntry
	configMap []metav1.ManagedFieldsEntry
}{
	namespace: []metav1.ManagedFieldsEntry{{
		Manager:   "Terraform",
		Operation: metav1.ManagedFieldsOperationApply,
		FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:annotations":{"f:note":{}},"f:labels":{"f:kubernetes.io/metadata.name":{},"f:tofu-estate":{}}},"f:spec":{"f:finalizers":{}}}`)},
	}},
	configMap: []metav1.ManagedFieldsEntry{
		{
			Manager:   "Terraform",
			Operation: metav1.ManagedFieldsOperationApply,
			FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:data":{"f:greeting":{}},"f:metadata":{"f:annotations":{"f:scraped":{}},"f:labels":{"f:tier":{},"f:tofu-estate":{}}}}`)},
		},
		{
			Manager:   "kubectl-annotate",
			Operation: metav1.ManagedFieldsOperationUpdate,
			FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:annotations":{"f:scraped":{}}}}`)},
		},
		{
			Manager:   "kubectl-label",
			Operation: metav1.ManagedFieldsOperationUpdate,
			FieldsV1:  &metav1.FieldsV1{Raw: []byte(`{"f:metadata":{"f:labels":{"f:owner":{}}}}`)},
		},
	},
}

// TestManagedMetadataKeysReportsLaunderedKeys pins the refutation above as
// a fact rather than a memory. This function is not wrong - it reports what
// the API server says - and that is precisely the point: what the API
// server says includes keys the provider echoed back rather than keys the
// configuration declared, so this set is not on its own a safe removal set.
//
// If a later change makes the provider stop echoing, or gives this package
// a narrower source, this test will start failing and the reader will find
// the measurement that put it here.
func TestManagedMetadataKeysReportsLaunderedKeys(t *testing.T) {
	ns, present := ManagedMetadataKeys(lbl1211Object(launderedEntries.namespace...), "Terraform", metadataMaps)
	if !present {
		t.Fatal("the laundered namespace fixture reported no managedFields")
	}
	if !ns["labels"]["kubernetes.io/metadata.name"] {
		t.Error("the API server's own namespace label is no longer reported as ours - if the provider stopped echoing computed_fields on apply, read GitHub issue #1211 and revisit the removal rule")
	}
	if len(launderedEntries.namespace) != 1 {
		t.Fatal("the fixture is meant to hold exactly one manager, which is what makes the co-ownership filter useless here")
	}

	cm, _ := ManagedMetadataKeys(lbl1211Object(launderedEntries.configMap...), "Terraform", metadataMaps)
	if !cm["annotations"]["scraped"] {
		t.Error("an annotation kubectl wrote is no longer reported as ours; see the comment on launderedEntries")
	}
	if cm["labels"]["owner"] {
		t.Error("the fixture was expected to show our claim on owner already dropped by an apply that changed the labels map")
	}
}
