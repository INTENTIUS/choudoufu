// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package marksafe

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// This file is the mutation evidence for issue #247. Every claim below is
// written as "this shape used to be accepted; here is the shape, and here is
// the assertion that it is not accepted now". A test that only asserted the
// current behaviour would pass just as well against the code that had the
// hole.

// ---------------------------------------------------------------------------
// The derivation
// ---------------------------------------------------------------------------

// TestNewlyDerivedMethodsReallyPanic is the EXTERNAL half of the derived set.
//
// [TestUnsafeMethodSetMatchesCty] compares the derivation's output against a
// list recorded from that same derivation, which is a ratchet measuring
// agreement with itself: mutate the derivation and the recorded list together
// and it stays green. So the four methods the derivation gained are asserted
// here by hand-written Go that calls them, on a marked value, the way a caller
// would - cty is the authority, and this is the only test in the package that
// consults it without going through reflection.
func TestNewlyDerivedMethodsReallyPanic(t *testing.T) {
	marked := cty.ListVal([]cty.Value{cty.StringVal("a")}).Mark("sensitive")
	markedStr := cty.StringVal("x").Mark("sensitive")

	cases := []struct {
		method string
		call   func()
		// invisible names the property of the OLD derivation that hid this
		// method, so that a regression of that property is described rather
		// than merely detected.
		invisible string
	}{
		{
			method: "Elements",
			call: func() {
				for range marked.Elements() {
					break
				}
			},
			invisible: "the panic is inside the returned iter.Seq2, so calling the method and looking at the result sees nothing",
		},
		{
			method: "ForEachElement",
			call: func() {
				marked.ForEachElement(func(k, v cty.Value) bool { return false })
			},
			invisible: "it takes an argument, and the derivation called only no-argument methods",
		},
		{
			method:    "Hash",
			call:      func() { markedStr.Hash() },
			invisible: "it panics from set_internals.go with its own wording, never through assertUnmarked",
		},
		{
			method:    "Range",
			call:      func() { markedStr.Range() },
			invisible: "it panics from value_range.go with its own wording, never through assertUnmarked",
		},
	}

	derived := UnsafeMethods()
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			msg, panicked := recoverMessage(tc.call)
			if !panicked {
				t.Fatalf("cty.Value.%s no longer panics on a marked receiver; if cty made it mark-safe, drop it from the recorded list", tc.method)
			}
			if !derived[tc.method] {
				t.Errorf("cty.Value.%s panics %q on a marked receiver but the derivation does not report it.\n"+
					"    It was invisible to the previous derivation because %s.", tc.method, msg, tc.invisible)
			}
		})
	}
}

// TestHashAndRangeDoNotUseCtysAssertUnmarked is the reason the derivation
// stopped matching on a message. If either of these ever raised
// markedPanicMessage, the message filter would have been sufficient after all
// and this test should be deleted along with the differential machinery.
func TestHashAndRangeDoNotUseCtysAssertUnmarked(t *testing.T) {
	marked := cty.StringVal("x").Mark("sensitive")
	for name, call := range map[string]func(){
		"Hash":  func() { marked.Hash() },
		"Range": func() { marked.Range() },
	} {
		msg, panicked := recoverMessage(call)
		if !panicked {
			t.Errorf("%s no longer panics on a marked receiver", name)
			continue
		}
		if msg == markedPanicMessage {
			t.Errorf("%s now raises cty's assertUnmarked message %q. The derivation no longer needs to compare outcomes to see it; simplify or delete this test.", name, msg)
		}
		if !strings.Contains(strings.ToLower(msg), "mark") {
			t.Errorf("%s panics %q, which does not mention marks at all; check it is still the mark that causes it", name, msg)
		}
	}
}

// TestDrivingIsWhatFindsElements defeats the derivation rather than reviewing
// it: with the iterator left undriven - which is what the previous version
// did - Elements does not panic and would not be derived.
func TestDrivingIsWhatFindsElements(t *testing.T) {
	marked := cty.ListVal([]cty.Value{cty.StringVal("a")}).Mark("sensitive")
	m := reflect.ValueOf(marked).MethodByName("Elements")

	if _, panicked := recoverMessage(func() { m.Call(nil) }); panicked {
		t.Fatal("calling Elements without driving the iterator now panics; driveIterator is no longer what makes it visible, so this test no longer proves anything")
	}
	if out := callOutcome(m, nil); out == noPanic {
		t.Error("callOutcome did not drive the returned iterator: Elements came back clean, which is exactly how it escaped the derived set")
	}
}

// TestSyntheticArgsReachArgumentTakingMethods is the other half: with no
// arguments invented, reflect refuses the call outright and ForEachElement is
// unreachable.
func TestSyntheticArgsReachArgumentTakingMethods(t *testing.T) {
	marked := cty.ListVal([]cty.Value{cty.StringVal("a")}).Mark("sensitive")
	rt := reflect.TypeOf(cty.Value{})
	m, ok := rt.MethodByName("ForEachElement")
	if !ok {
		t.Fatal("cty.Value has no ForEachElement")
	}
	if m.Type.NumIn() == 1 {
		t.Fatal("ForEachElement now takes no arguments; the no-argument filter would have found it and this test no longer proves anything")
	}
	args := syntheticArgs(m.Type)
	if len(args) != m.Type.NumIn()-1 {
		t.Fatalf("syntheticArgs invented %d arguments for a method taking %d", len(args), m.Type.NumIn()-1)
	}
	if out := callOutcome(reflect.ValueOf(marked).MethodByName("ForEachElement"), args); out != markedPanicMessage {
		t.Errorf("ForEachElement on a marked receiver came back %q, want cty's assertUnmarked panic", out)
	}
}

func recoverMessage(f func()) (msg string, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			msg, panicked = fmt.Sprint(r), true
		}
	}()
	f()
	return "", false
}

// ---------------------------------------------------------------------------
// The proof shapes
// ---------------------------------------------------------------------------

// scanSource plants one file and returns its sites. No receiver index, so
// every method name matches: these files have no hcl in them.
func scanSource(t *testing.T, src string) []Site {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "planted.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sites, err := Scan([]string{dir}, UnsafeMethods(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return sites
}

func onlySite(t *testing.T, src string) Site {
	t.Helper()
	sites := scanSource(t, src)
	if len(sites) != 1 {
		t.Fatalf("planted a file with one unsafe call and the scanner found %d: %v", len(sites), sites)
	}
	return sites[0]
}

// TestUnsoundProofShapesAreRejected is issue #247's second group. Each case
// is a shape [Scan] answered "guarded by an IsMarked test on the same value"
// for, when no such test governs the read. A recorded proof that does not
// hold is worse than an admitted gap, because the entire value of this
// package is that a green result means something.
//
// Every case carries a `fixed` twin differing only in the detail that makes
// the guard real. Without it these would pass against a scanner that had
// simply stopped recognising guards - the failure mode this package's own doc
// warns about.
func TestUnsoundProofShapesAreRejected(t *testing.T) {
	cases := []struct {
		name   string
		broken string
		fixed  string
	}{
		{
			name: "empty guard body",
			// The check never looked at control flow, so a test that did
			// nothing licensed every read below it.
			broken: `
func read(v cty.Value) string {
	if v.IsMarked() {
	}
	return v.AsString()
}`,
			fixed: `
func read(v cty.Value) string {
	if v.IsMarked() {
		return ""
	}
	return v.AsString()
}`,
		},
		{
			name: "guard body falls through",
			// A body that does not leave says nothing about the code after
			// it, however much it does inside.
			broken: `
func read(v cty.Value) string {
	out := ""
	if v.IsMarked() {
		out = "sensitive"
	}
	return out + v.AsString()
}`,
			fixed: `
func read(v cty.Value) string {
	out := ""
	if v.IsMarked() {
		return "sensitive"
	}
	return out + v.AsString()
}`,
		},
		{
			name: "reassignment after a guard",
			// The guarded value is gone; the name holds something else.
			broken: `
func read(v, other cty.Value) string {
	if v.IsMarked() {
		return ""
	}
	v = other
	return v.AsString()
}`,
			fixed: `
func read(v, other cty.Value) string {
	if v.IsMarked() {
		return ""
	}
	_ = other
	return v.AsString()
}`,
		},
		{
			name: "guard in a sibling branch",
			// Position ordering accepted a test the read never runs after.
			broken: `
func read(v cty.Value, flag bool) string {
	if flag {
		if v.IsMarked() {
			return ""
		}
	} else {
		return v.AsString()
	}
	return ""
}`,
			fixed: `
func read(v cty.Value, flag bool) string {
	if v.IsMarked() {
		return ""
	}
	if flag {
		return ""
	}
	return v.AsString()
}`,
		},
		{
			name: "different call receivers compared equal",
			// exprString rendered every call as fn(...), so a guard on one
			// call proved a read of another.
			broken: `
func read(m map[string]cty.Value) string {
	if get(m, "safe").IsMarked() {
		return ""
	}
	return get(m, "danger").AsString()
}

func get(m map[string]cty.Value, k string) cty.Value { return m[k] }`,
			fixed: `
func read(m map[string]cty.Value) string {
	if get(m, "danger").IsMarked() {
		return ""
	}
	return get(m, "danger").AsString()
}

func get(m map[string]cty.Value, k string) cty.Value { return m[k] }`,
		},
		{
			name: "else-if guard reached by a then-branch that falls through",
			// Control can arrive below the chain without the else's
			// condition ever being evaluated, so the guard in it governs
			// nothing there.
			broken: `
func read(v cty.Value, flag bool) string {
	out := ""
	if flag {
		out = "x"
	} else if v.IsMarked() {
		return ""
	}
	return out + v.AsString()
}`,
			fixed: `
func read(v cty.Value, flag bool) string {
	out := ""
	if flag {
		return "x"
	} else if v.IsMarked() {
		return ""
	}
	return out + v.AsString()
}`,
		},
		{
			name: "guard scoped to an inner block",
			// A guard inside a loop body proves nothing after the loop.
			broken: `
func read(vs []cty.Value, v cty.Value) string {
	for range vs {
		if v.IsMarked() {
			continue
		}
	}
	return v.AsString()
}`,
			fixed: `
func read(vs []cty.Value, v cty.Value) string {
	for range vs {
		_ = v
	}
	if v.IsMarked() {
		return ""
	}
	return v.AsString()
}`,
		},
	}

	const header = "package planted\n\nimport \"github.com/zclconf/go-cty/cty\"\n"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			broken := onlySite(t, header+tc.broken+"\n")
			if broken.Proof != "" {
				t.Errorf("this shape is proven %q, and nothing in it establishes that:\n%s", broken.Proof, tc.broken)
			}
			fixed := onlySite(t, header+tc.fixed+"\n")
			if fixed.Proof != ProofGuarded {
				t.Errorf("the repaired shape is proven %q, want %q. The rule now refuses a guard that works, which is a worse error than the one it was fixing:\n%s",
					fixed.Proof, ProofGuarded, tc.fixed)
			}
		})
	}
}

// TestGuardsThatMustKeepWorking is the other direction, and the one that
// caught a first attempt at this rule. Tightening what counts as a proof is
// only correct if it refuses nothing that works, and Go's boolean operators
// mean a guard beside a read in one condition is as real as a guard above it.
//
// Reported when the || case was wrong: 26 call sites across 11 packages.
func TestGuardsThatMustKeepWorking(t *testing.T) {
	const header = "package planted\n\nimport \"github.com/zclconf/go-cty/cty\"\n"
	cases := map[string]string{
		"guard among || operands, refusing body": `
func read(v cty.Value, err error) string {
	if err != nil || v.IsNull() || v.IsMarked() {
		return ""
	}
	return v.AsString()
}`,
		"guard short-circuits the read beside it": `
func read(v cty.Value, err error, key string) bool {
	return err == nil && !v.IsNull() && !v.IsMarked() && v.AsString() == key
}`,
		"guard among || operands, read beside it": `
func read(v cty.Value, key string) bool {
	if v.IsNull() || v.IsMarked() || v.AsString() != key {
		return false
	}
	return true
}`,
		"negated guard opening a body": `
func read(v cty.Value) string {
	if v.Type() == cty.String && !v.IsMarked() {
		return v.AsString()
	}
	return ""
}`,
		"guard with a continue inside a loop": `
func read(vs []cty.Value) []string {
	var out []string
	for _, v := range vs {
		if v.IsMarked() {
			continue
		}
		out = append(out, v.AsString())
	}
	return out
}`,
		"read in the else of a guard": `
func read(v cty.Value) string {
	if v.IsMarked() {
		return ""
	} else {
		return v.AsString()
	}
}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			s := onlySite(t, header+body+"\n")
			if s.Proof != ProofGuarded {
				t.Errorf("a working guard is reported %q, want %q:\n%s", s.Proof, ProofGuarded, body)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The receiver index
// ---------------------------------------------------------------------------

// TestReceiverIndexIsNotBlind is the audit shape that has caught real defects
// in this repository three times: a check that reports everything is fine
// because it can see nothing. [ProofNotCtyValue] proves a site by resolving
// its receiver, so an index that resolved everything to "not a cty.Value" -
// or that was silently empty and combined with a scanner that skipped what it
// could not resolve - would pass the whole tree while checking none of it.
//
// Three separate assertions, because each fails differently:
//
//  1. The index resolves real cty.Value receivers AS cty.Value, so it is not
//     dismissing the sites this package exists for.
//  2. Every site it dismisses names the type it dismissed them for.
//  3. Removing the index turns sites unproven rather than leaving them
//     proven, so resolution is what licenses them and not an absence.
func TestReceiverIndexIsNotBlind(t *testing.T) {
	idx := receiverIndex(t)
	if len(idx) == 0 {
		t.Fatal("the receiver index is empty, so no site can be dismissed by type and every Range() call should be unproven; if TestNoUnprovenUnsafeCallSite passed, it is passing blind")
	}

	sites, err := Scan(packageDirs(t, guardedPackages), UnsafeMethods(), idx)
	if err != nil {
		t.Fatal(err)
	}

	var cty, foreign, unresolved int
	for _, s := range sites {
		switch {
		case s.RecvType == "":
			unresolved++
		case isCtyValue(s.RecvType):
			cty++
			if s.Proof == ProofNotCtyValue {
				t.Errorf("%s has receiver type %s and is dismissed as not a cty.Value", s, s.RecvType)
			}
		default:
			foreign++
			if s.Proof != ProofNotCtyValue {
				continue // proven some other way, which is fine
			}
		}
	}
	if cty == 0 {
		t.Error("the index resolved no receiver at all to cty.Value across every guarded package, which cannot be true while these packages read cty values")
	}
	if foreign == 0 {
		t.Error("the index resolved no receiver to anything other than cty.Value, so ProofNotCtyValue is doing no work and the collision it exists for has gone; check whether Range is still derived")
	}
	if unresolved != 0 {
		t.Errorf("%d site(s) have no resolved receiver type. Unresolved is treated as unproven, so this is not unsound, but it means the index is not covering the files Scan reads.", unresolved)
	}

	// Mutation: without the index, the same sites lose the proof.
	blind, err := Scan(packageDirs(t, guardedPackages), UnsafeMethods(), nil)
	if err != nil {
		t.Fatal(err)
	}
	lost := 0
	for _, s := range blind {
		if s.Proof == "" {
			lost++
		}
	}
	if lost == 0 {
		t.Error("dropping the receiver index changed nothing, so no site depends on it and ProofNotCtyValue is decorative")
	}
	t.Logf("%d sites on cty.Value, %d dismissed by type, %d unproven without the index", cty, foreign, lost)
}

// TestReceiverIndexResolvesTheCollision pins the specific fact that made the
// index necessary: two methods called Range, one of which is mark-unsafe.
func TestReceiverIndexResolvesTheCollision(t *testing.T) {
	if !UnsafeMethods()["Range"] {
		t.Skip("Range is no longer derived as mark-unsafe; the collision it created is moot")
	}
	sites, err := Scan(packageDirs(t, guardedPackages), UnsafeMethods(), receiverIndex(t))
	if err != nil {
		t.Fatal(err)
	}
	hcl := 0
	for _, s := range sites {
		if s.Method != "Range" {
			continue
		}
		if isCtyValue(s.RecvType) {
			continue
		}
		if !strings.Contains(s.RecvType, "hcl") {
			t.Errorf("%s: Range on unexpected receiver type %s", s, s.RecvType)
		}
		hcl++
	}
	if hcl == 0 {
		t.Error("no Range() call resolves to an hcl type, which is what the receiver index was added for")
	}
	t.Logf("%d Range() calls belong to hcl, not cty", hcl)
}
