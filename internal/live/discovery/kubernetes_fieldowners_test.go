// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// fieldOwnerReader is a kubesweep.ObjectReader holding one object, or an
// error for every read.
type fieldOwnerReader struct {
	obj *unstructured.Unstructured
	err error
}

func (r fieldOwnerReader) ReadObject(_ context.Context, _ kubesweep.ObjectRef) (*unstructured.Unstructured, bool, error) {
	if r.err != nil {
		return nil, false, r.err
	}
	return r.obj, r.obj != nil, nil
}

func sharedConfigMap(managers map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("v1")
	obj.SetKind("ConfigMap")
	obj.SetNamespace("ns")
	obj.SetName("shared")
	var entries []metav1.ManagedFieldsEntry
	for m, fields := range managers {
		entries = append(entries, metav1.ManagedFieldsEntry{
			Manager:   m,
			Operation: metav1.ManagedFieldsOperationApply,
			FieldsV1:  &metav1.FieldsV1{Raw: []byte(fields)},
		})
	}
	obj.SetManagedFields(entries)
	return obj
}

func labelWrite(t *testing.T, addr string, force bool, keys ...string) FieldGranularWrite {
	t.Helper()
	w := kubesweep.FieldWrite{Root: []string{"f:metadata", "f:labels"}}
	for _, k := range keys {
		w.Members = append(w.Members, kubesweep.MapMember(k))
	}
	return FieldGranularWrite{
		Addr:   mustAddr(t, addr),
		Object: kubesweep.ObjectRef{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns", Name: "shared"},
		Writes: []kubesweep.FieldWrite{w},
		Force:  force,
	}
}

func onlySummary(t *testing.T, diags tfdiags.Diagnostics, want string) string {
	t.Helper()
	if got := diagSummaries(diags); len(got) != 1 || got[0] != want {
		t.Fatalf("diagnostics = %v, want exactly [%s]", got, want)
	}
	return diags[0].Description().Detail
}

// TestForceAcrossEstatesIsRefusedByName is #1106 section 3's second
// control (GitHub issue #1191): force = true over a label another estate's
// field manager owns is an error naming that estate.
func TestForceAcrossEstatesIsRefusedByName(t *testing.T) {
	reader := fieldOwnerReader{obj: sharedConfigMap(map[string]string{
		"choudoufu:alpha": `{"f:metadata":{"f:labels":{"f:owner":{}}}}`,
	})}
	diags := CheckKubernetesFieldOwners(context.Background(), reader, nil, "beta", []FieldGranularWrite{labelWrite(t, "kubernetes_labels.owner", true, "owner")})
	if !diags.HasErrors() {
		t.Fatalf("force across estates was not refused: %v", diagSummaries(diags))
	}
	detail := onlySummary(t, diags, SummaryFieldForceAcrossEstates)
	for _, want := range []string{`the estate "alpha" owns`, `"choudoufu:alpha"`, "kubernetes_labels.owner", "f:owner"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not say %q: %s", want, detail)
		}
	}
}

// TestUnforcedWriteOverAnotherEstateWarns: the same write without force is
// a warning naming the estate, because the API server refuses it at apply.
func TestUnforcedWriteOverAnotherEstateWarns(t *testing.T) {
	reader := fieldOwnerReader{obj: sharedConfigMap(map[string]string{
		"choudoufu:alpha": `{"f:metadata":{"f:labels":{"f:owner":{}}}}`,
	})}
	diags := CheckKubernetesFieldOwners(context.Background(), reader, nil, "beta", []FieldGranularWrite{labelWrite(t, "kubernetes_labels.owner", false, "owner")})
	if diags.HasErrors() {
		t.Fatalf("an unforced write was refused: %v", diagSummaries(diags))
	}
	if detail := onlySummary(t, diags, SummaryFieldOwnedByEstate); !strings.Contains(detail, `the estate "alpha" owns`) {
		t.Errorf("the warning does not name alpha: %s", detail)
	}
}

// TestForceAgainstANonEstateManagerKeepsItsMeaning: force over a field
// kubectl, the provider's default manager or a controller owns is not this
// pass's business, and neither is this estate's own manager, nor another
// estate's field the write does not touch.
func TestForceAgainstANonEstateManagerKeepsItsMeaning(t *testing.T) {
	reader := fieldOwnerReader{obj: sharedConfigMap(map[string]string{
		"kubectl-label":   `{"f:metadata":{"f:labels":{"f:team":{}}}}`,
		"Terraform":       `{"f:metadata":{"f:labels":{"f:owner":{}}}}`,
		"choudoufu:beta":  `{"f:metadata":{"f:labels":{"f:owner":{}}}}`,
		"choudoufu:alpha": `{"f:metadata":{"f:labels":{"f:elsewhere":{}}}}`,
	})}
	diags := CheckKubernetesFieldOwners(context.Background(), reader, nil, "beta", []FieldGranularWrite{labelWrite(t, "kubernetes_labels.owner", true, "owner", "team")})
	if len(diags) != 0 {
		t.Errorf("force over non-estate managers raised %v", diagSummaries(diags))
	}
}

// TestFieldOwnersUnreadableIsAGap: an object that cannot be read back is a
// warning, never a refusal and never silence.
func TestFieldOwnersUnreadableIsAGap(t *testing.T) {
	diags := CheckKubernetesFieldOwners(context.Background(), fieldOwnerReader{err: errors.New("boom")}, nil, "beta", []FieldGranularWrite{labelWrite(t, "kubernetes_labels.owner", true, "owner")})
	onlySummary(t, diags, SummaryFieldOwnersUnavailable)
	if diags.HasErrors() {
		t.Error("an unreadable object stopped the plan")
	}
}

// TestTwoBlocksOnOneObjectAreRefused: one estate, one manager, two blocks
// on one object - each apply would erase the other's fields.
func TestTwoBlocksOnOneObjectAreRefused(t *testing.T) {
	a := labelWrite(t, "kubernetes_labels.a", false, "a")
	b := labelWrite(t, "kubernetes_annotations.b", false, "b")
	other := labelWrite(t, "kubernetes_labels.c", false, "c")
	other.Object.Name = "elsewhere"
	diags := SameObjectFieldWrites(nil, "beta", []FieldGranularWrite{a, b, other})
	detail := onlySummary(t, diags, SummaryFieldGranularSameObject)
	for _, want := range []string{"kubernetes_annotations.b, kubernetes_labels.a", `"choudoufu:beta"`} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not say %q: %s", want, detail)
		}
	}
	if strings.Contains(detail, "kubernetes_labels.c") {
		t.Errorf("a block on another object was named: %s", detail)
	}
}
