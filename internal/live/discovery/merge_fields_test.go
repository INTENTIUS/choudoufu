// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"reflect"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// mergePassLocal is every [Result] field [Merge] deliberately does not carry
// from a pass into the merged result, with the reason. Every other field a
// pass sets must reach the merged result: GitHub issue #1780 was
// DeposedBindings missing from Merge, which left a deposed object a pass had
// settled handled by nobody - neither bound, nor an orphan, nor proposed for
// destroy - on every estate with two provider configurations.
//
// A new field lands here only with a reason a reader can check. "The merge
// forgot it" is not one: carry it in [Merge] instead.
var mergePassLocal = map[string]string{
	"Estate":             "set once by Merge from its own argument, not copied from any pass",
	"OtherEstateHeld":    "read inside the pass that listed it (parent_read.go's foreign-parent check) before Merge runs; nothing reads it off a merged result",
	"RecordedElsewhere":  "consumed by Merge itself (refuseRecordedProviderAbsent reads every pass's own slice), which turns it into the merged Problems",
	"OwnerSkipped":       "a per-pass count nothing outside the pass reads",
	"NativeSweepSkipped": "reported from the single-pass result only; a known reporting gap, not an ownership decision",
	"Guided":             "a per-pass report flag; a known reporting gap, not an ownership decision",
	"GuidedFallback":     "a per-pass report line; a known reporting gap, not an ownership decision",
	"GuidedSweepSkipped": "a per-pass report list; a known reporting gap, not an ownership decision",
}

// TestMergeCarriesEveryPassField sets each [Result] field, one at a time, on
// the second of two passes and asserts it survives [Merge]. Run against the
// Merge #1780 fixed, it fails on DeposedBindings.
func TestMergeCarriesEveryPassField(t *testing.T) {
	addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_config_map", Name: "x"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	samples := map[reflect.Type]reflect.Value{
		reflect.TypeOf(addrs.AbsResourceInstance{}): reflect.ValueOf(addr),
		reflect.TypeOf(identity.Resolution{}):       reflect.ValueOf(identity.Resolution{Addr: addr, Class: identity.ClassConcrete, ImportID: "ns/x"}),
		reflect.TypeOf(projection.DeposedBinding{}): reflect.ValueOf(projection.DeposedBinding{Addr: addr, DeposedKey: "00000001", ImportID: "ns/x"}),
		reflect.TypeOf(Binding{}):                   reflect.ValueOf(Binding{Addr: addr, TypeName: "kubernetes_config_map", ImportID: "ns/x"}),
		reflect.TypeOf(OwnedResource{}):             reflect.ValueOf(OwnedResource{Addr: addr, TypeName: "kubernetes_config_map", ImportID: "ns/x"}),
		reflect.TypeOf(SlotAssignment{}):            reflect.ValueOf(SlotAssignment{Addr: addr}),
		reflect.TypeOf(Problem{}):                   reflect.ValueOf(Problem{Kind: ProblemCollision, TypeName: "kubernetes_config_map", Addr: addr}),
	}
	var fill func(t reflect.Type) reflect.Value
	fill = func(t reflect.Type) reflect.Value {
		if v, ok := samples[t]; ok {
			return v
		}
		v := reflect.New(t).Elem()
		switch t.Kind() {
		case reflect.Slice:
			v = reflect.MakeSlice(t, 1, 1)
			v.Index(0).Set(fill(t.Elem()))
		case reflect.Map:
			v = reflect.MakeMap(t)
			v.SetMapIndex(fill(t.Key()), fill(t.Elem()))
		case reflect.String:
			v.SetString("x")
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Int, reflect.Int64, reflect.Int32:
			v.SetInt(1)
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				if t.Field(i).IsExported() {
					v.Field(i).Set(fill(t.Field(i).Type))
				}
			}
		}
		return v
	}

	rt := reflect.TypeOf(Result{})
	seen := map[string]bool{}
	var fields []reflect.StructField
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Anonymous {
			for j := 0; j < f.Type.NumField(); j++ {
				sub := f.Type.Field(j)
				sub.Index = []int{i, j}
				fields = append(fields, sub)
			}
			continue
		}
		fields = append(fields, f)
	}
	if len(fields) < 20 {
		t.Fatalf("enumerated %d Result fields; the walk over the embedded Verdicts and Report is blind", len(fields))
	}
	for _, f := range fields {
		if !f.IsExported() {
			continue
		}
		seen[f.Name] = true
		if _, local := mergePassLocal[f.Name]; local {
			continue
		}
		t.Run(f.Name, func(t *testing.T) {
			second := &Result{}
			reflect.ValueOf(second).Elem().FieldByIndex(f.Index).Set(fill(f.Type))
			merged, _, _ := Merge(estateName, []Pass{
				{Provider: testProviderAddr(t, "east"), Result: &Result{}},
				{Provider: testProviderAddr(t, "west"), Result: second},
			}, false)
			if reflect.ValueOf(merged).Elem().FieldByIndex(f.Index).IsZero() {
				t.Errorf("Result.%s set on a pass is empty after Merge: carry it in Merge, or list it in mergePassLocal with the reason nothing reads it off a merged result", f.Name)
			}
		})
	}
	for name := range mergePassLocal {
		if !seen[name] {
			t.Errorf("mergePassLocal lists %s, which Result no longer has", name)
		}
	}
}
