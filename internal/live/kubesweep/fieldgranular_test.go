// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"bytes"
	"reflect"
	"sort"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/structured-merge-diff/v6/fieldpath"
)

// paths renders an entry's FieldsV1 as sorted path strings, so a test can
// compare ownership without depending on JSON key order.
func paths(t *testing.T, e metav1.ManagedFieldsEntry) []string {
	t.Helper()
	s := &fieldpath.Set{}
	if err := s.FromJSON(bytes.NewReader(e.FieldsV1.Raw)); err != nil {
		t.Fatalf("decoding %s: %s", e.Manager, err)
	}
	var out []string
	s.Iterate(func(p fieldpath.Path) { out = append(out, p.String()) })
	sort.Strings(out)
	return out
}

func entryOf(t *testing.T, entries []metav1.ManagedFieldsEntry, manager string) (metav1.ManagedFieldsEntry, bool) {
	t.Helper()
	for _, e := range entries {
		if e.Manager == manager && e.Subresource == "" {
			return e, true
		}
	}
	return metav1.ManagedFieldsEntry{}, false
}

// TestTransferFieldOwnershipMovesOnlyTheWritesFields (GitHub issue #1863,
// the migration hand-over): the keys the write names move from
// "Terraform" to the estate's Apply entry, created from Terraform's; a
// key Terraform wrote that the write does not name stays Terraform's; a
// third manager's entry - another estate's - is returned untouched even
// where it owns the same key.
func TestTransferFieldOwnershipMovesOnlyTheWritesFields(t *testing.T) {
	entries := []metav1.ManagedFieldsEntry{
		fmEntry("Terraform", "", `{"f:metadata":{"f:labels":{".":{},"f:team":{},"f:extra":{}}}}`),
		fmEntry("choudoufu:other", "", `{"f:metadata":{"f:labels":{"f:team":{}}}}`),
		fmEntry("kube-controller", "status", `{"f:status":{}}`),
	}
	write := FieldWrite{Root: []string{"f:metadata", "f:labels"}, Members: []string{MapMember("team")}}
	out, changed, err := TransferFieldOwnership(entries, "Terraform", "choudoufu:me", []FieldWrite{write})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v; want the label handed over", changed, err)
	}
	me, ok := entryOf(t, out, "choudoufu:me")
	if !ok || me.Operation != metav1.ManagedFieldsOperationApply {
		t.Fatalf("no Apply entry for choudoufu:me in %+v", out)
	}
	if got, want := paths(t, me), []string{".metadata.labels.team"}; !reflect.DeepEqual(got, want) {
		t.Errorf("choudoufu:me owns %v, want %v", got, want)
	}
	tf, _ := entryOf(t, out, "Terraform")
	if got, want := paths(t, tf), []string{".metadata.labels", ".metadata.labels.extra"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Terraform keeps %v, want %v", got, want)
	}
	other, _ := entryOf(t, out, "choudoufu:other")
	if !reflect.DeepEqual(other, entries[1]) {
		t.Errorf("another estate's entry changed: %+v", other)
	}
	if len(out) != 4 {
		t.Errorf("entries = %d, want the three plus the new Apply entry", len(out))
	}
}

// TestTransferFieldOwnershipTakesAnEnvItemWhole: an env item's name and
// value move with the item, and an emptied Terraform entry is dropped.
func TestTransferFieldOwnershipTakesAnEnvItemWhole(t *testing.T) {
	entries := []metav1.ManagedFieldsEntry{
		fmEntry("Terraform", "", `{"f:spec":{"f:template":{"f:spec":{"f:containers":{"k:{\"name\":\"app\"}":{"f:env":{"k:{\"name\":\"LOG\"}":{".":{},"f:name":{},"f:value":{}}}}}}}}}`),
		fmEntry("choudoufu:me", "", `{"f:metadata":{"f:annotations":{"f:x":{}}}}`),
	}
	write := FieldWrite{Root: EnvRoot("Deployment", "app", false), Members: []string{ListItemMember(map[string]string{"name": "LOG"})}}
	out, changed, err := TransferFieldOwnership(entries, "Terraform", "choudoufu:me", []FieldWrite{write})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, ok := entryOf(t, out, "Terraform"); ok {
		t.Errorf("Terraform's emptied entry was kept: %+v", out)
	}
	me, _ := entryOf(t, out, "choudoufu:me")
	got := paths(t, me)
	for _, want := range []string{
		`.metadata.annotations.x`,
		`.spec.template.spec.containers[name="app"].env[name="LOG"]`,
		`.spec.template.spec.containers[name="app"].env[name="LOG"].name`,
		`.spec.template.spec.containers[name="app"].env[name="LOG"].value`,
	} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("choudoufu:me does not own %s; owns %v", want, got)
		}
	}
}

// TestTransferFieldOwnershipTakesAnAtomicRoot: a Node's taints, owned as
// one leaf, move as that leaf.
func TestTransferFieldOwnershipTakesAnAtomicRoot(t *testing.T) {
	entries := []metav1.ManagedFieldsEntry{fmEntry("Terraform", "", `{"f:spec":{"f:taints":{}}}`)}
	write := FieldWrite{Root: TaintsRoot, Members: []string{ListItemMember(map[string]string{"key": "k", "effect": "NoSchedule"})}}
	out, changed, err := TransferFieldOwnership(entries, "Terraform", "choudoufu:me", []FieldWrite{write})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	me, _ := entryOf(t, out, "choudoufu:me")
	if got, want := paths(t, me), []string{".spec.taints"}; !reflect.DeepEqual(got, want) {
		t.Errorf("choudoufu:me owns %v, want %v", got, want)
	}
}

// TestTransferFieldOwnershipIsANoOpWhenFromOwnsNothing: nothing moves, and
// the same slice comes back, when the source manager owns none of the
// write - a second run of a migration, or an object stock never wrote.
func TestTransferFieldOwnershipIsANoOpWhenFromOwnsNothing(t *testing.T) {
	entries := []metav1.ManagedFieldsEntry{fmEntry("choudoufu:me", "", `{"f:metadata":{"f:labels":{"f:team":{}}}}`)}
	write := FieldWrite{Root: []string{"f:metadata", "f:labels"}, Members: []string{MapMember("team")}}
	out, changed, err := TransferFieldOwnership(entries, "Terraform", "choudoufu:me", []FieldWrite{write})
	if err != nil || changed || !reflect.DeepEqual(out, entries) {
		t.Errorf("changed=%v err=%v out=%+v; want no change", changed, err, out)
	}
	if _, changed, _ := TransferFieldOwnership(entries, "choudoufu:me", "choudoufu:me", []FieldWrite{write}); changed {
		t.Error("a transfer from a manager to itself changed something")
	}
}

// TestManagerFieldsUnionsApplyEntriesOnly: Update entries and subresource
// entries under the name are not the manager's apply-owned fields.
func TestManagerFieldsUnionsApplyEntriesOnly(t *testing.T) {
	update := fmEntry("choudoufu:me", "", `{"f:metadata":{"f:labels":{"f:u":{}}}}`)
	update.Operation = metav1.ManagedFieldsOperationUpdate
	entries := []metav1.ManagedFieldsEntry{
		fmEntry("choudoufu:me", "", `{"f:metadata":{"f:labels":{"f:a":{}}}}`),
		update,
		fmEntry("choudoufu:me", "status", `{"f:status":{}}`),
	}
	raw, ok, err := ManagerFields(entries, "choudoufu:me")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	members, _, found := OwnedAt(raw, []string{"f:metadata", "f:labels"})
	if !found || !reflect.DeepEqual(members, []string{"f:a"}) {
		t.Errorf("owned labels = %v, want only the Apply entry's", members)
	}
	if _, ok, _ := ManagerFields(entries, "Terraform"); ok {
		t.Error("a manager with no entries owns something")
	}
}

// TestEnvContainersReadsWhichContainersEnvIsOwned: containers and init
// containers are told apart, and a container whose env the manager does
// not own is not named.
func TestEnvContainersReadsWhichContainersEnvIsOwned(t *testing.T) {
	raw := []byte(`{"f:spec":{"f:containers":{"k:{\"name\":\"app\"}":{"f:env":{"k:{\"name\":\"A\"}":{}}},"k:{\"name\":\"side\"}":{"f:image":{}}},"f:initContainers":{"k:{\"name\":\"init\"}":{"f:env":{"k:{\"name\":\"B\"}":{}}}}}}`)
	names, inits := EnvContainers(raw, "Pod")
	if !reflect.DeepEqual(names, []string{"app"}) || !reflect.DeepEqual(inits, []string{"init"}) {
		t.Errorf("EnvContainers = %v, %v; want [app], [init]", names, inits)
	}
}

// TestFieldGranularFixedKind: the three fixed-kind types patch the core
// kind their name starts with.
func TestFieldGranularFixedKind(t *testing.T) {
	for typeName, want := range map[string]string{
		"kubernetes_config_map_v1_data": "ConfigMap",
		"kubernetes_secret_v1_data":     "Secret",
		"kubernetes_node_taint":         "Node",
	} {
		if v, k := FieldGranularFixedKind(typeName); v != "v1" || k != want {
			t.Errorf("FieldGranularFixedKind(%s) = %s %s, want v1 %s", typeName, v, k, want)
		}
	}
}
