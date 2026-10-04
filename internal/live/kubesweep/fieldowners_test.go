// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func fmObject(entries ...metav1.ManagedFieldsEntry) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("ConfigMap")
	obj.SetName("shared")
	obj.SetNamespace("ns")
	obj.SetManagedFields(entries)
	return obj
}

func fmEntry(manager, subresource, fields string) metav1.ManagedFieldsEntry {
	return metav1.ManagedFieldsEntry{
		Manager:     manager,
		Operation:   metav1.ManagedFieldsOperationApply,
		APIVersion:  "v1",
		FieldsType:  "FieldsV1",
		FieldsV1:    &metav1.FieldsV1{Raw: []byte(fields)},
		Subresource: subresource,
	}
}

// TestFieldOwnersNamesWhoOwnsWhat (GitHub issue #1191): the managers that
// own a member of the write are named with exactly the members they own;
// the excluded manager (the writing estate's own), a manager owning only
// other keys, and a subresource entry are not.
func TestFieldOwnersNamesWhoOwnsWhat(t *testing.T) {
	obj := fmObject(
		fmEntry("choudoufu:a", "", `{"f:metadata":{"f:labels":{".":{},"f:owner":{}}}}`),
		fmEntry("kubectl-label", "", `{"f:metadata":{"f:labels":{"f:team":{}}}}`),
		fmEntry("choudoufu:b", "", `{"f:metadata":{"f:labels":{"f:other":{}}}}`),
		fmEntry("choudoufu:me", "", `{"f:metadata":{"f:labels":{"f:owner":{}}}}`),
		fmEntry("status-writer", "status", `{"f:metadata":{"f:labels":{"f:owner":{}}}}`),
	)
	write := FieldWrite{Root: []string{"f:metadata", "f:labels"}, Members: []string{MapMember("owner"), MapMember("team")}}
	got := FieldOwners(obj, write, "choudoufu:me")
	want := []FieldOwner{
		{Manager: "choudoufu:a", Members: []string{"f:owner"}},
		{Manager: "kubectl-label", Members: []string{"f:team"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FieldOwners =\n %+v\nwant\n %+v", got, want)
	}
}

// TestFieldOwnersReadsAnAtomicListAsOwnedWhole: a Node's spec.taints is
// atomic, which managedFields records as one leaf; every manager holding
// it owns the whole list, whatever members the write names.
func TestFieldOwnersReadsAnAtomicListAsOwnedWhole(t *testing.T) {
	obj := fmObject(fmEntry("choudoufu:a", "", `{"f:spec":{"f:taints":{}}}`))
	write := FieldWrite{Root: []string{"f:spec", "f:taints"}, Members: []string{ListItemMember(map[string]string{"key": "k", "effect": "NoSchedule"})}, Atomic: true}
	got := FieldOwners(obj, write, "choudoufu:me")
	if len(got) != 1 || got[0].Manager != "choudoufu:a" || !got[0].Atomic {
		t.Errorf("FieldOwners = %+v, want choudoufu:a owning spec.taints whole", got)
	}
}

// TestListItemMemberSpellsTheServersKey: the spelling the API server writes
// for an associative-list item, keys sorted by field name.
func TestListItemMemberSpellsTheServersKey(t *testing.T) {
	if got, want := ListItemMember(map[string]string{"name": "app"}), `k:{"name":"app"}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got, want := ListItemMember(map[string]string{"key": "k", "effect": "NoSchedule"}), `k:{"effect":"NoSchedule","key":"k"}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// TestFieldOwnersFindsAContainersEnv: the env of one container, keyed by
// name under the pod template, is found down the whole path, and another
// container's env is not this write's.
func TestFieldOwnersFindsAContainersEnv(t *testing.T) {
	obj := fmObject(fmEntry("choudoufu:a", "", `{"f:spec":{"f:template":{"f:spec":{"f:containers":{
		"k:{\"name\":\"app\"}":{"f:env":{"k:{\"name\":\"LOG\"}":{".":{},"f:value":{}}}},
		"k:{\"name\":\"side\"}":{"f:env":{"k:{\"name\":\"MODE\"}":{}}}}}}}}`))
	root := []string{"f:spec", "f:template", "f:spec", "f:containers", ListItemMember(map[string]string{"name": "app"}), "f:env"}
	got := FieldOwners(obj, FieldWrite{Root: root, Members: []string{ListItemMember(map[string]string{"name": "LOG"}), ListItemMember(map[string]string{"name": "MODE"})}}, "choudoufu:me")
	if len(got) != 1 || !reflect.DeepEqual(got[0].Members, []string{`k:{"name":"LOG"}`}) {
		t.Errorf("FieldOwners = %+v, want choudoufu:a owning LOG alone (MODE is the other container's)", got)
	}
}

// TestFieldOwnersSkipsAReleasedEntry (#1885): hashicorp/kubernetes
// deletes a field-granular block with an apply of the empty map (or env
// list), which leaves the manager's entry owning the empty container -
// {"f:metadata":{"f:annotations":{}}}. labels, annotations, data and env
// are granular, so that entry owns no key: read as atomic, it named a
// released estate as the owner of every key another estate then wrote
// there. Only a write whose root may be atomic (a Node's taints) reads
// a memberless leaf as owned whole.
func TestFieldOwnersSkipsAReleasedEntry(t *testing.T) {
	obj := fmObject(
		fmEntry("choudoufu:released", "", `{"f:metadata":{"f:annotations":{}}}`),
		fmEntry("choudoufu:holder", "", `{"f:metadata":{"f:annotations":{"f:k":{}}}}`),
	)
	write := FieldWrite{Root: []string{"f:metadata", "f:annotations"}, Members: []string{MapMember("k")}}
	got := FieldOwners(obj, write, "choudoufu:me")
	want := []FieldOwner{{Manager: "choudoufu:holder", Members: []string{"f:k"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FieldOwners =\n %+v\nwant\n %+v: a released entry owns no key", got, want)
	}
}
