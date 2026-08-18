// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package marksafe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// liveRoot is internal/live, relative to this package's own directory.
const liveRoot = ".."

// guardedPackages are the packages this check holds to zero unproven sites.
//
// They are the ones that evaluate CONFIGURATION: every value they read can
// carry a mark, because `variable "x" { sensitive = true }` is an ordinary
// thing to write and the mark propagates through every expression derived
// from it. That is the population issue #240's eight panics came from.
var guardedPackages = []string{
	"acceptance",
	"check",
	"cloudcontrol",
	"dataread",
	"discovery",
	// Issue #256 item 8's godoc citation sweep: parses Go source with
	// go/ast for symbol names and touches no cty.Value at all, so like
	// "onboard" below it is held to zero rather than deferred - it has
	// nothing to defer.
	"docrefs",
	"docsref",
	"flocitest",
	"foreign",
	"harness",
	"identity",
	"lifecycle",
	"lint",
	"markerkey",
	"mdspan",
	"moved",
	// Rewrites a module's source text and touches no cty.Value at all, so
	// it is held to zero rather than deferred: it has nothing to defer.
	"onboard",
	"passthrough",
	"pins",
	"pluginschema",
	"policy",
	"projection",
	"providerscope",
	"providerversion",
	"refusalscan",
	"registry",
	"slots",
	"stamp",
	"staterecord",
	"uniquename",
}

// deferredPackages read CLOUD OBJECTS - a live read's response, a prior
// state value - rather than configuration expressions. A mark can reach one
// (a provider schema may declare an attribute sensitive) but no path from a
// sensitive input variable to one has been demonstrated, and the three
// copies of equivalent/zeroish/mapElements in them are 42 of the 45 sites.
//
// This is debt with an address, not an exemption: [TestDeferredSitesAreExact]
// pins every deferred site individually, so a NEW unproven call in one of
// these packages fails this check the same as anywhere else. What is
// deferred is the existing inventory, not the package.
var deferredPackages = []string{
	"listclient",
	"liveimport",
	"markers",
	"mv",
	"untag",
}

func packageDirs(t *testing.T, names []string) []string {
	t.Helper()
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, filepath.Join(liveRoot, n))
	}
	return out
}

// moduleRoot is the repository root, relative to this package's directory.
const moduleRoot = "../../.."

// receiverIndex type-checks every package under internal/live once, so the
// scanner can tell cty.Value.Range from hcl.Expression.Range. Built once per
// process because the load is the expensive part of these tests.
var (
	sharedIndex     ReceiverIndex
	sharedIndexErr  error
	sharedIndexOnce sync.Once
)

func receiverIndex(t *testing.T) ReceiverIndex {
	t.Helper()
	sharedIndexOnce.Do(func() {
		sharedIndex, sharedIndexErr = LoadReceiverIndex(moduleRoot, "./internal/live/...")
	})
	if sharedIndexErr != nil {
		t.Fatalf("building the receiver index: %v\n"+
			"Without it every Range() call in these packages is reported unproven, so this is fatal rather than a fallback.", sharedIndexErr)
	}
	return sharedIndex
}

// TestEveryLivePackageIsClassified is the guard against the failure mode
// that has appeared three times in this repository already: a check whose
// scope is a list nobody updates. A package added under internal/live must
// join guardedPackages or deferredPackages, and adding it to the second is
// a decision someone has to write down.
func TestEveryLivePackageIsClassified(t *testing.T) {
	entries, err := os.ReadDir(liveRoot)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, n := range append(append([]string{}, guardedPackages...), deferredPackages...) {
		known[n] = true
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "marksafe" || e.Name() == "testdata" {
			continue
		}
		found = append(found, e.Name())
		if !known[e.Name()] {
			t.Errorf("internal/live/%s is in neither guardedPackages nor deferredPackages. "+
				"Add it to the first if its unsafe cty calls are all proven, or to the second with the reason.", e.Name())
		}
	}
	for n := range known {
		if !containsStr(found, n) {
			t.Errorf("internal/live/%s is listed here but no longer exists; remove it.", n)
		}
	}
}

func containsStr(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// TestUnsafeMethodSetMatchesCty locks the derived set to what this file
// records. The set itself is computed by calling cty, so the recorded list
// is a record and cty is the authority: a cty release that makes another
// accessor behave differently on a marked receiver fails here, and the fix
// is to update the list and re-run the scan, which will then have more sites
// to prove.
//
// The list is here rather than in the scanner because the scanner must
// never consult it: a hand list inside the derivation is exactly the thing
// that missed False, which turned out to be three of issue #240's eight
// sites.
//
// On its own this is a ratchet measuring agreement with itself - mutate the
// derivation and this list together and it stays green.
// [TestNewlyDerivedMethodsReallyPanic] is the external half, calling the four
// methods this list gained in issue #247 as ordinary Go and asserting cty
// panics.
func TestUnsafeMethodSetMatchesCty(t *testing.T) {
	want := []string{
		"AsBigFloat",
		"AsString",
		"AsValueMap",
		"AsValueSet",
		"AsValueSlice",
		"ElementIterator",
		"Elements",
		"EncapsulatedValue",
		"False",
		"ForEachElement",
		"Hash",
		"LengthInt",
		"Range",
		"True",
	}
	got := sortedKeys(UnsafeMethods())
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("cty's mark-unsafe methods are now %v, recorded here as %v.\n"+
			"Update the recorded list and re-run TestNoUnprovenUnsafeCallSite: a method entering this set brings call sites with it.", got, want)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestOnlySetsHoistElementMarks is the cty fact three of the fixes turn on,
// asserted against cty rather than described in a comment: marking an
// element of a list, map, object or tuple leaves the CONTAINER unmarked,
// so a guard on the container does not make its elements readable. A set
// is the exception, because set membership cannot be decided with marks in
// play, so cty lifts them to the set itself.
//
// Four call sites in this repository read elements after testing only the
// container. Two of them were reachable and panicked.
func TestOnlySetsHoistElementMarks(t *testing.T) {
	sec := cty.StringVal("s").Mark("sensitive")
	cases := []struct {
		name       string
		build      func() cty.Value
		wantHoists bool
	}{
		{"list", func() cty.Value { return cty.ListVal([]cty.Value{sec}) }, false},
		{"map", func() cty.Value { return cty.MapVal(map[string]cty.Value{"a": sec}) }, false},
		{"object", func() cty.Value { return cty.ObjectVal(map[string]cty.Value{"a": sec}) }, false},
		{"tuple", func() cty.Value { return cty.TupleVal([]cty.Value{sec}) }, false},
		{"set", func() cty.Value { return cty.SetVal([]cty.Value{sec}) }, true},
	}
	for _, tc := range cases {
		v := tc.build()
		if v.IsMarked() != tc.wantHoists {
			t.Errorf("%s container IsMarked = %v, want %v", tc.name, v.IsMarked(), tc.wantHoists)
			continue
		}
		if tc.wantHoists {
			continue
		}
		// The container is unmarked, so an ordinary guard passes it - and
		// the element under it still panics.
		panicked := func() (p bool) {
			defer func() {
				if r := recover(); r != nil {
					p = fmt.Sprint(r) == markedPanicMessage
				}
			}()
			for it := v.ElementIterator(); it.Next(); {
				_, ev := it.Element()
				if ev.Type() == cty.String {
					_ = ev.AsString()
				}
			}
			return false
		}()
		if !panicked {
			t.Errorf("%s: reading an element of an unmarked container no longer panics; the element guards added for issue #240 may be unnecessary", tc.name)
		}
	}
}

// TestIteratorKeysAreNeverMarked is the other cty fact the scanner relies
// on: ProofIteratorKey licenses reading the key half of an element without
// a guard, on the grounds that cty synthesizes it. Asserted rather than
// assumed, because 14 call sites are proven by it.
func TestIteratorKeysAreNeverMarked(t *testing.T) {
	sec := cty.StringVal("s").Mark("sensitive")
	for name, v := range map[string]cty.Value{
		"map":    cty.MapVal(map[string]cty.Value{"a": sec}),
		"object": cty.ObjectVal(map[string]cty.Value{"a": sec}),
		"list":   cty.ListVal([]cty.Value{sec}),
		"tuple":  cty.TupleVal([]cty.Value{sec}),
	} {
		for it := v.ElementIterator(); it.Next(); {
			k, _ := it.Element()
			if k.IsMarked() {
				t.Errorf("%s: an element key is marked; ProofIteratorKey is no longer sound", name)
			}
		}
	}
}

// TestNoUnprovenUnsafeCallSite is the check itself.
func TestNoUnprovenUnsafeCallSite(t *testing.T) {
	sites, err := Scan(packageDirs(t, guardedPackages), UnsafeMethods(), receiverIndex(t))
	if err != nil {
		t.Fatal(err)
	}
	proven := 0
	for _, s := range sites {
		if s.Proof == "" {
			t.Errorf("%s reads a value nothing here proves is unmarked.\n"+
				"    cty panics rather than errors on a marked receiver, and a sensitive input variable is the ordinary way to produce one.\n"+
				"    The fix is a guard that REFUSES - never an Unmark - because a marked value must not become an identity component or a cloud tag.", s)
			continue
		}
		proven++
	}
	t.Logf("%d call sites of %d mark-unsafe cty methods across %d packages, all proven",
		proven, len(UnsafeMethods()), len(guardedPackages))
}

// TestDeferredSitesAreExact pins the deferred inventory site by site, so
// that listing a package above bounds WHAT is deferred rather than only
// WHO. A new unproven call in one of those packages is a new key here and
// fails; a site that has been fixed or deleted is a stale key and also
// fails, so the list cannot outlive the debt it records.
//
// Keyed by package, function, receiver, method and COUNT rather than by
// line, so that editing the file above one of them is not a diff here while
// a fourth a.LengthInt() inside equivalent still is. A key without a count
// would bound who, not what - the shape that let a cohort allowlist accept
// unlimited new drift.
func TestDeferredSitesAreExact(t *testing.T) {
	sites, err := Scan(packageDirs(t, deferredPackages), UnsafeMethods(), receiverIndex(t))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, s := range sites {
		if s.Proof != "" {
			continue
		}
		counts[deferredKey(s)]++
	}
	got := map[string]bool{}
	for k, n := range counts {
		got[fmt.Sprintf("%s x%d", k, n)] = true
	}
	want := map[string]bool{}
	for _, k := range deferredSites {
		want[k] = true
	}
	for k := range got {
		if !want[k] {
			t.Errorf("new unproven unsafe cty call: %s\n"+
				"    These packages carry an inventory of existing debt, not a licence to add to it. Prove the receiver unmarked, or refuse it.", k)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("deferredSites lists %s, which no longer exists; remove the entry.", k)
		}
	}
	t.Logf("%d deferred unproven sites across %d packages", len(got), len(deferredPackages))
}

func deferredKey(s Site) string {
	return fmt.Sprintf("%s %s %s.%s()", filepath.Base(filepath.Dir(s.File))+"/"+filepath.Base(s.File), s.Func, s.Recv, s.Method)
}

// deferredSites is the inventory described on [deferredPackages]. Every
// entry reads a value that came from a cloud read or a prior state object
// rather than from a configuration expression.
//
// The three near-identical copies of equivalent, zeroish and mapElements in
// mv, untag and liveimport account for 42 of these. Folding them into one
// implementation and proving that one is the shape of the follow-up; doing
// it here would be a refactor of three command paths in a change whose
// subject is a crash class.
var deferredSites = []string{
	"listclient/listclient.go IdentityAttr v.AsString() x1",

	"liveimport/tags.go equivalent a.ElementIterator() x1",
	"liveimport/tags.go equivalent a.LengthInt() x3",
	"liveimport/tags.go equivalent b.HasIndex(k).False() x1",
	"liveimport/tags.go equivalent b.LengthInt() x2",
	"liveimport/tags.go mapElements val.ElementIterator() x2",
	"liveimport/tags.go mapElements val.LengthInt() x1",
	"liveimport/tags.go tagsFromObject val.AsString() x1",
	"liveimport/tags.go zeroish v.AsString() x1",
	"liveimport/tags.go zeroish v.False() x1",
	"liveimport/tags.go zeroish v.LengthInt() x1",

	"markers/markers.go TagsOf v.ElementIterator() x1",
	"markers/markers.go TagsOf val.AsString() x1",

	"mv/rewrite.go equivalent a.ElementIterator() x1",
	"mv/rewrite.go equivalent a.LengthInt() x3",
	"mv/rewrite.go equivalent b.HasIndex(k).False() x1",
	"mv/rewrite.go equivalent b.LengthInt() x2",
	"mv/rewrite.go mapElements val.ElementIterator() x2",
	"mv/rewrite.go mapElements val.LengthInt() x1",
	"mv/rewrite.go tagsFromListed val.AsString() x1",
	"mv/rewrite.go zeroish v.AsString() x1",
	"mv/rewrite.go zeroish v.False() x1",
	"mv/rewrite.go zeroish v.LengthInt() x1",

	"untag/tags.go equivalent a.ElementIterator() x1",
	"untag/tags.go equivalent a.LengthInt() x3",
	"untag/tags.go equivalent b.HasIndex(k).False() x1",
	"untag/tags.go equivalent b.LengthInt() x2",
	"untag/tags.go mapElements val.ElementIterator() x2",
	"untag/tags.go mapElements val.LengthInt() x1",
	"untag/tags.go zeroish v.AsString() x1",
	"untag/tags.go zeroish v.False() x1",
	"untag/tags.go zeroish v.LengthInt() x1",
}

// TestScannerSeesAPlantedSite defeats the scanner rather than reviewing it:
// a scanner that has stopped recognising call sites reports that everything
// is proven, which is the exact failure internal/live/refusalscan was
// defeated by four ways in one sitting. So a file with a known-unprovable
// call is written to a temporary directory and the scanner must find it.
func TestScannerSeesAPlantedSite(t *testing.T) {
	dir := t.TempDir()
	src := `package planted

import "github.com/zclconf/go-cty/cty"

func read(v cty.Value) string {
	if v.IsNull() {
		return ""
	}
	return v.AsString()
}

func alsoRead(v cty.Value) bool {
	return v.False()
}
`
	if err := os.WriteFile(filepath.Join(dir, "planted.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sites, err := Scan([]string{dir}, UnsafeMethods(), nil)
	if err != nil {
		t.Fatal(err)
	}
	unproven := 0
	for _, s := range sites {
		if s.Proof == "" {
			unproven++
		}
	}
	if unproven != 2 {
		t.Fatalf("the scanner found %d unproven sites in a file with two; it has stopped seeing call sites: %v", unproven, sites)
	}

	// And the guard has to be what makes the difference, not the file's
	// existence: adding one turns the same site proven.
	guarded := strings.Replace(src, "if v.IsNull() {", "if v.IsNull() || v.IsMarked() {", 1)
	guarded = strings.Replace(guarded, "return v.False()", "if v.IsMarked() {\n\t\treturn false\n\t}\n\treturn v.False()", 1)
	if err := os.WriteFile(filepath.Join(dir, "planted.go"), []byte(guarded), 0o644); err != nil {
		t.Fatal(err)
	}
	sites, err = Scan([]string{dir}, UnsafeMethods(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		if s.Proof != ProofGuarded {
			t.Errorf("%s is proven %q after a guard was added, want %q", s, s.Proof, ProofGuarded)
		}
	}
}

// TestAGuardBelowItsReadIsNotAProof is the other way to defeat the
// scanner: write the IsMarked test after the accessor it is supposed to
// license. A function-scope rule accepts that, which is why the proof
// carries a position.
func TestAGuardBelowItsReadIsNotAProof(t *testing.T) {
	dir := t.TempDir()
	src := `package planted

import "github.com/zclconf/go-cty/cty"

func backwards(v cty.Value) (string, bool) {
	s := v.AsString()
	if v.IsMarked() {
		return "", false
	}
	return s, true
}
`
	if err := os.WriteFile(filepath.Join(dir, "backwards.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	sites, err := Scan([]string{dir}, UnsafeMethods(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Fatalf("found %d sites, want 1: %v", len(sites), sites)
	}
	if sites[0].Proof != "" {
		t.Errorf("a guard written BELOW the read proved it (%q); the position rule has stopped working", sites[0].Proof)
	}
}
