// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
)

// A set-typed module variable binds a for-comprehension's KEY variable to the
// element, not to a position. cty's element iterator synthesizes a StringVal
// for a map key or an object attribute name and a NumberIntVal for a list or
// tuple index, but for a set it hands back the element itself in BOTH halves
// (cty/element_iterator.go), and hclsyntax.ForExpr.Value binds e.KeyVar to
// whatever it is handed. The chase through a module variable read the CALL's
// argument - a tuple - and answered with the tuple's indices, so
// `{ for k, v in var.s : "n-${k}" => v }` over a variable declared
// set(string) produced n-0/n-1 where OpenTofu produces one instance per
// element named after that element.
//
// Every assertion below is on a rendered import ID and on the whole set of
// addresses resolved, never on a predicate. The wrong answer had the RIGHT
// COUNT and the wrong names, which is exactly the shape a boolean misses.

// childInstanceIDs lists every instance inside module.child, address to import
// ID, so an extra instance, a missing one and a wrong ID are all visible in
// one comparison.
func childInstanceIDs(result *Result) map[string]string {
	out := map[string]string{}
	for _, res := range result.All() {
		if addr := res.Addr.String(); strings.HasPrefix(addr, "module.child.") {
			out[addr] = res.ImportID
		}
	}
	return out
}

func assertChildIDs(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	cfg := loadConfigTree(t, filepath.Join("testdata", dir), nil)
	result, _ := ResolveIn(context.Background(), cfg, CloudContext{AccountID: "000000000000", Region: "us-east-1"})
	got := childInstanceIDs(result)
	if len(got) != len(want) {
		t.Errorf("%s resolved %d instances in module.child, want %d\n got: %s\nwant: %s",
			dir, len(got), len(want), formatIDs(got), formatIDs(want))
	}
	for addr, id := range want {
		if got[addr] != id {
			t.Errorf("%s: %s import ID is %q, want %q", dir, addr, got[addr], id)
		}
	}
	for addr, id := range got {
		if _, expected := want[addr]; !expected {
			t.Errorf("%s: %s resolved to import ID %q; it was not expected to resolve at all", dir, addr, id)
		}
	}
}

// TestSetVarWholeEvaluationIsTheOracle pins what OpenTofu actually produces
// for the child module both these fixtures use, through the one path that was
// never in doubt: the argument evaluates whole, so
// [resolver.evaluatedCollElements] reads cty's own element iterator and the
// keys are the elements. It is the reference the chase has to agree with, and
// it fails if the fix disturbs the path that already worked.
func TestSetVarWholeEvaluationIsTheOracle(t *testing.T) {
	assertChildIDs(t, "setkey-whole", map[string]string{
		`module.child.aws_iam_user.u["n-e-alpha"]`:  "n-e-alpha",
		`module.child.aws_iam_user.u["n-e-beta"]`:   "n-e-beta",
		`module.child.aws_iam_group.g["n-e-alpha"]`: "g-e-alpha",
		`module.child.aws_iam_group.g["n-e-beta"]`:  "g-e-beta",
	})
}

// TestSetVarUnreadableElementRefuses is the reproduction. The same child, one
// argument element this resolver cannot read - a managed resource's attribute,
// which is exactly what forces the chase instead of whole evaluation.
//
// Before the fix this answered TWO instances, module.child.aws_iam_user.u
// ["n-0"] and ["n-1"], with import IDs n-0 and n-1 - the tuple's indices, and
// markers naming users OpenTofu never creates. It must resolve nothing: a
// set's keys are its elements, and an element this cannot read is not a key it
// can name. The count is the reason a partial answer is not on offer either,
// since two unreadable elements are indistinguishable and collapse into one.
func TestSetVarUnreadableElementRefuses(t *testing.T) {
	assertChildIDs(t, "setkey-dynamic", map[string]string{})
}

// TestObjectVarOptionalAttributeIsAKey is the other shape the iterator read
// moves, and it moves in the same direction. An object-typed variable's
// optional attribute that the call leaves out is still an attribute of the
// value the module sees - prepareFinalInputVariableValue applies the type
// defaults before anything inside the module runs - so `for_each = var.s`
// expands it. The key set was previously the CALL's own attribute names, which
// is one instance short.
func TestObjectVarOptionalAttributeIsAKey(t *testing.T) {
	assertChildIDs(t, "setkey-objdefault", map[string]string{
		`module.child.aws_iam_user.u["a"]`: "a",
		`module.child.aws_iam_user.u["b"]`: "b",
		`module.child.aws_iam_user.u["c"]`: "c",
	})
}

// ---- varConvertedElems, directly ----------------------------------------

// valBindings lifts a plain values slice into the bindings [varConvertedElems]
// takes since #260. Every one of these cases is about the VALUE half, so no
// element expression is supplied; the expression half has its own tests in
// eachvalue_test.go.
func valBindings(vals []cty.Value) []elemBinding {
	out := make([]elemBinding, len(vals))
	for i, v := range vals {
		out[i] = elemBinding{val: v}
	}
	return out
}

func setVar(t *testing.T, ty cty.Type) *configs.Variable {
	t.Helper()
	return &configs.Variable{Name: "s", Type: ty, ConstraintType: ty}
}

// elemPairs renders a keys/vals answer as "key=value" strings so a test can
// compare the PAIRING, not only the two sides separately. cty.NilVal - this
// file's "not proven" signal - renders as a bare key.
func elemPairs(keys []cty.Value, elems []elemBinding) []string {
	out := make([]string, 0, len(keys))
	for i, k := range keys {
		s := k.GoString()
		if i < len(elems) && elems[i].val != cty.NilVal {
			s += "=" + elems[i].val.GoString()
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func assertElems(t *testing.T, gotKeys []cty.Value, gotElems []elemBinding, gotOK bool, wantOK bool, want ...string) {
	t.Helper()
	if gotOK != wantOK {
		t.Fatalf("ok is %v, want %v", gotOK, wantOK)
	}
	if !wantOK {
		return
	}
	got := elemPairs(gotKeys, gotElems)
	if len(got) != len(want) {
		t.Fatalf("answered %d elements %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("element %d is %s, want %s (whole answer %v)", i, got[i], want[i], got)
		}
	}
}

// TestSetElemsAreKeysAndValues: a tuple argument reaching a set(string)
// declaration. Both halves of every pair are the element, which is what an
// element iterator over a set yields and what a comprehension therefore binds.
func TestSetElemsAreKeysAndValues(t *testing.T) {
	keys := []cty.Value{cty.NumberIntVal(0), cty.NumberIntVal(1)}
	vals := []cty.Value{cty.StringVal("beta"), cty.StringVal("alpha")}
	gotKeys, gotElems, ok := varConvertedElems(setVar(t, cty.Set(cty.String)), keys, valBindings(vals))
	assertElems(t, gotKeys, gotElems, ok, true,
		`cty.StringVal("alpha")=cty.StringVal("alpha")`,
		`cty.StringVal("beta")=cty.StringVal("beta")`,
	)
}

// TestSetNumberIndexCoincidence is the mutation the positional read could not
// survive. [0, 5] converted to set(number) is {0, 5}: the elements ARE
// numbers, so element 0 RawEquals the tuple index 0 and element 5 matches
// nothing, and a read that pairs converted elements against the chase's own
// keys silently keeps the indices [0, 1] as the key set. The right answer has
// key 5 in it and no key 1 at all.
func TestSetNumberIndexCoincidence(t *testing.T) {
	keys := []cty.Value{cty.NumberIntVal(0), cty.NumberIntVal(1)}
	vals := []cty.Value{cty.NumberIntVal(0), cty.NumberIntVal(5)}
	gotKeys, gotElems, ok := varConvertedElems(setVar(t, cty.Set(cty.Number)), keys, valBindings(vals))
	assertElems(t, gotKeys, gotElems, ok, true,
		`cty.NumberIntVal(0)=cty.NumberIntVal(0)`,
		`cty.NumberIntVal(5)=cty.NumberIntVal(5)`,
	)
	for _, k := range gotKeys {
		if k.RawEquals(cty.NumberIntVal(1)) {
			t.Errorf("key 1 is the tuple's second INDEX, not an element of {0, 5}: %v", elemPairs(gotKeys, gotElems))
		}
	}
}

// TestSetDedupesTheCount: a set of two equal elements is one element, so
// OpenTofu creates one instance. Answering two - which the index read did,
// because a tuple has two positions whatever is in them - is the wrong-marker
// shape with the count wrong as well.
func TestSetDedupesTheCount(t *testing.T) {
	keys := []cty.Value{cty.NumberIntVal(0), cty.NumberIntVal(1)}
	vals := []cty.Value{cty.StringVal("same"), cty.StringVal("same")}
	gotKeys, gotElems, ok := varConvertedElems(setVar(t, cty.Set(cty.String)), keys, valBindings(vals))
	assertElems(t, gotKeys, gotElems, ok, true, `cty.StringVal("same")=cty.StringVal("same")`)
}

// TestSetWithUnreadableElementRefuses: an element the chase could not prove
// enters the conversion as cty.DynamicVal and comes back unknown. Two unknowns
// are RawEquals to each other and collapse into one, so an unreadable element
// costs the COUNT and not only its own key. A set answers nothing there.
func TestSetWithUnreadableElementRefuses(t *testing.T) {
	keys := []cty.Value{cty.NumberIntVal(0), cty.NumberIntVal(1)}
	vals := []cty.Value{cty.StringVal("alpha"), cty.NilVal}
	_, _, ok := varConvertedElems(setVar(t, cty.Set(cty.String)), keys, valBindings(vals))
	if ok {
		t.Error("a set with an element this could not read answered a key set; it has none")
	}
}

// TestSetFailedConversionRefuses: an object is not convertible to a set at
// all, so OpenTofu rejects the configuration. The keys in hand are the
// object's attribute names, which are not the set's elements under any
// reading, so this refuses rather than passing them through - the one place
// where a failed conversion costs the keys as well as the values.
func TestSetFailedConversionRefuses(t *testing.T) {
	keys := []cty.Value{cty.StringVal("a"), cty.StringVal("b")}
	vals := []cty.Value{cty.StringVal("x"), cty.StringVal("y")}
	_, _, ok := varConvertedElems(setVar(t, cty.Set(cty.String)), keys, valBindings(vals))
	if ok {
		t.Error("an object that does not convert to a set answered a key set")
	}
}

// TestKeyedTargetsKeepTheirKeys is the other side of the rule, and the one
// that would break if "read the iterator" were applied without asking what the
// target keeps. A map, an object, a list and a tuple all keep the keys they
// were given, so the answer here must be the same keys with #251's conversion
// applied to the values - "007" reaching map(number) is the number 7.
func TestKeyedTargetsKeepTheirKeys(t *testing.T) {
	t.Run("map", func(t *testing.T) {
		keys := []cty.Value{cty.StringVal("a"), cty.StringVal("b")}
		vals := []cty.Value{cty.StringVal("007"), cty.NilVal}
		gotKeys, gotElems, ok := varConvertedElems(setVar(t, cty.Map(cty.Number)), keys, valBindings(vals))
		assertElems(t, gotKeys, gotElems, ok, true,
			`cty.StringVal("a")=cty.NumberIntVal(7)`,
			`cty.StringVal("b")`,
		)
	})
	t.Run("list", func(t *testing.T) {
		keys := []cty.Value{cty.NumberIntVal(0), cty.NumberIntVal(1)}
		vals := []cty.Value{cty.StringVal("007"), cty.StringVal("008")}
		gotKeys, gotElems, ok := varConvertedElems(setVar(t, cty.List(cty.Number)), keys, valBindings(vals))
		assertElems(t, gotKeys, gotElems, ok, true,
			`cty.NumberIntVal(0)=cty.NumberIntVal(7)`,
			`cty.NumberIntVal(1)=cty.NumberIntVal(8)`,
		)
	})
	t.Run("failed conversion keeps the keys", func(t *testing.T) {
		keys := []cty.Value{cty.StringVal("a")}
		vals := []cty.Value{cty.StringVal("not-a-number")}
		gotKeys, gotElems, ok := varConvertedElems(setVar(t, cty.Map(cty.Number)), keys, valBindings(vals))
		assertElems(t, gotKeys, gotElems, ok, true, `cty.StringVal("a")`)
	})
}
