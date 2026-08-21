// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"fmt"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

// A module step's instance key used to decode as [addrs.StringKey] whatever
// it looked like, on the premise that count on a module block was refused
// permanently so a for_each was the only thing that could ever put a key
// there. Issue #195 admitted a statically-evaluable count that does not leak
// count.index, and internal/live/stamp resolves a count of exactly 1 and
// writes "module.counted[0].aws_vpc.main" - so the premise was gone and the
// decode was not. The marker escaped to "module.counted:0.aws_vpc.main" and
// came back as module.counted["0"].aws_vpc.main.
//
// These tests are in this package rather than in internal/live/discovery,
// where the older UnescapeAddress tests sit against the thin re-export,
// because this is the package that owns the grammar and the one that has to
// keep the two key positions answering the same question.

// TestUnescapeAddress_countedModuleStepKeepsItsIndex is the defect itself, on
// the exact address internal/live/stamp writes for
// live/e2e/limits/child-module/counted.
//
// The address is BUILT rather than spelled: addrs renders it, this package
// escapes it, this package unescapes it, and the comparison is between two
// addrs renderings. A test that spelled "module.counted[0].aws_vpc.main" on
// both sides would be pinning this file's opinion of the notation instead of
// the round trip.
func TestUnescapeAddress_countedModuleStepKeepsItsIndex(t *testing.T) {
	in := addrs.AbsResourceInstance{
		Module: addrs.RootModuleInstance.Child("counted", addrs.IntKey(0)),
		Resource: addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_vpc",
			Name: "main",
		}.Instance(addrs.NoKey),
	}

	escaped := EscapeAddress(in.String())
	back, ok := UnescapeAddress(escaped)
	if !ok {
		t.Fatalf("%s escapes to %q, which does not unescape at all", in, escaped)
	}
	if got := back.String(); got != in.String() {
		t.Errorf("marker %q for %s comes back as %s; a count'd module's marker must recover the index it was stamped with, not a string key of the same digits",
			escaped, in, got)
	}
	if _, isInt := back.Module[0].InstanceKey.(addrs.IntKey); !isInt {
		t.Errorf("the module step's key came back as %T, want addrs.IntKey - the rendered address can agree while the key type does not, and the key type is what a module expansion is matched on",
			back.Module[0].InstanceKey)
	}
}

// TestUnescapeAddress_moduleQualifiedRoundTrip is the property the root-module
// sweep in internal/live/discovery already asserts, extended over the module
// prefix it never covered: that sweep generates only root addresses, so every
// module step in this grammar was unmeasured.
//
// Both directions are checked, and they are not the same property:
//
//   - escape(unescape(m)) == m has to hold for every valid marker, because
//     removal planning unescapes a marker to get an address to print and to
//     enter the prior state at. This direction held even with the wrong key
//     type - escaping is lossy about the key's type in exactly the same way
//     both readings are - which is precisely why nothing caught the defect.
//   - unescape(escape(a)) == a is the one that failed, and it is the one an
//     operator sees: the address in the plan has to be the address in the
//     configuration.
func TestUnescapeAddress_moduleQualifiedRoundTrip(t *testing.T) {
	moduleSteps := [][]addrs.ModuleInstanceStep{
		{{Name: "net", InstanceKey: addrs.NoKey}},
		{{Name: "counted", InstanceKey: addrs.IntKey(0)}},
		{{Name: "counted", InstanceKey: addrs.IntKey(7)}},
		{{Name: "counted", InstanceKey: addrs.IntKey(10)}},
		{{Name: "keyed", InstanceKey: addrs.StringKey("a")}},
		{{Name: "keyed", InstanceKey: addrs.StringKey("eu-west-1a")}},
		{{Name: "keyed", InstanceKey: addrs.StringKey("a.b")}},
		{{Name: "keyed", InstanceKey: addrs.StringKey("a:b")}},
		{{Name: "keyed", InstanceKey: addrs.StringKey("a@b")}},
		{{Name: "keyed", InstanceKey: addrs.StringKey("a(b)")}},
		{{Name: "outer", InstanceKey: addrs.NoKey}, {Name: "inner", InstanceKey: addrs.IntKey(0)}},
		{{Name: "outer", InstanceKey: addrs.IntKey(0)}, {Name: "inner", InstanceKey: addrs.StringKey("a")}},
		{{Name: "outer", InstanceKey: addrs.StringKey("a")}, {Name: "inner", InstanceKey: addrs.IntKey(3)}},
	}
	resourceKeys := []addrs.InstanceKey{
		addrs.NoKey,
		addrs.IntKey(0),
		addrs.IntKey(12),
		addrs.StringKey("a"),
		addrs.StringKey("a.b"),
	}

	checked := 0
	for _, steps := range moduleSteps {
		for _, rk := range resourceKeys {
			in := addrs.AbsResourceInstance{
				Module: addrs.ModuleInstance(steps),
				Resource: addrs.Resource{
					Mode: addrs.ManagedResourceMode,
					Type: "aws_subnet",
					Name: "this",
				}.Instance(rk),
			}

			escaped := EscapeAddress(in.String())
			if !ValidMarkerAddress(escaped) {
				t.Errorf("%s escapes to %q, which the marker grammar rejects", in, escaped)
				continue
			}
			back, ok := UnescapeAddress(escaped)
			if !ok {
				t.Errorf("%s escapes to %q, which does not unescape at all", in, escaped)
				continue
			}
			if again := EscapeAddress(back.String()); again != escaped {
				t.Errorf("marker %q unescapes to %s, which escapes back to %q - removal would label a destroy with an address whose marker is a different string",
					escaped, back, again)
			}
			if got := back.String(); got != in.String() {
				t.Errorf("%s escapes to %q and comes back as %s", in, escaped, got)
			}
			checked++
		}
	}
	if checked != len(moduleSteps)*len(resourceKeys) {
		t.Fatalf("checked %d addresses, want %d", checked, len(moduleSteps)*len(resourceKeys))
	}
	t.Logf("%d module-qualified addresses round-tripped", checked)
}

// TestUnescapeAddress_moduleDigitKeyAmbiguityIsTheResourceOne records what the
// fix trades away, so the trade is on the record rather than made silently.
//
// A count'd module step and a for_each'd module step whose key is the digit
// string of the same number escape to one marker, exactly as a count'd
// resource and a digit-string-keyed resource always have. The reading is the
// count one, at both positions, and at the module position the reason is
// stronger than at the resource position: internal/live/stamp writes a
// digit-keyed module step only for a count'd call, since resources inside a
// for_each'd call are SkipModuleKeyed and get no stamp at all
// (live/LIMITATIONS.md, "Resources inside a keyed module need hand-written
// markers"). A hand-written marker for a for_each'd module keyed "0" is the
// only thing on the other side of the coincidence, and it can mislabel but
// never misbind: a declared instance binds by [AddressMatches] comparing two
// escaped strings, which is the assertion at the end of this test, and never
// reaches [UnescapeAddress] at all.
func TestUnescapeAddress_moduleDigitKeyAmbiguityIsTheResourceOne(t *testing.T) {
	res := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_vpc", Name: "main"}

	counted := addrs.AbsResourceInstance{
		Module:   addrs.RootModuleInstance.Child("app", addrs.IntKey(0)),
		Resource: res.Instance(addrs.NoKey),
	}
	keyed := addrs.AbsResourceInstance{
		Module:   addrs.RootModuleInstance.Child("app", addrs.StringKey("0")),
		Resource: res.Instance(addrs.NoKey),
	}

	countedMarker := EscapeAddress(counted.String())
	keyedMarker := EscapeAddress(keyed.String())
	if countedMarker != keyedMarker {
		t.Fatalf("%s and %s escape to %q and %q; this test exists only because they collide",
			counted, keyed, countedMarker, keyedMarker)
	}

	back, ok := UnescapeAddress(countedMarker)
	if !ok {
		t.Fatalf("%q does not unescape", countedMarker)
	}
	if back.String() != counted.String() {
		t.Errorf("%q decodes to %s, want the count reading %s", countedMarker, back, counted)
	}

	// The half that makes the coincidence harmless: ownership is decided by
	// comparing escaped strings, and the for_each'd address the decode did
	// NOT pick still matches its own marker.
	if !AddressMatches(keyedMarker, keyed.String()) {
		t.Errorf("AddressMatches(%q, %q) is false; a declared for_each'd instance must still own its own marker whatever the decode reads it as",
			keyedMarker, keyed.String())
	}
	if !AddressMatches(countedMarker, counted.String()) {
		t.Errorf("AddressMatches(%q, %q) is false", countedMarker, counted.String())
	}
}

// TestDecodeInstanceKey_isOneAnswerForBothPositions is the structural half:
// the module step and the resource segment must not be able to drift again,
// so they are asserted to produce the same key for the same escaped text.
//
// The comparison is between two full addresses that differ only in WHERE the
// key sits, which is what makes a future edit to one branch of
// [UnescapeAddress] visible here even if it looks locally reasonable.
func TestDecodeInstanceKey_isOneAnswerForBothPositions(t *testing.T) {
	for _, key := range []string{"0", "1", "10", "007", "0x10", "-1", "+1", "a", "a-b", "eu-west-1a", "4294967295"} {
		atModule := fmt.Sprintf("module.m:%s.aws_vpc.main", EscapeKey(key))
		atResource := fmt.Sprintf("aws_vpc.main:%s", EscapeKey(key))

		modAddr, modOK := UnescapeAddress(atModule)
		resAddr, resOK := UnescapeAddress(atResource)
		if modOK != resOK {
			t.Errorf("key %q: module position ok=%v, resource position ok=%v", key, modOK, resOK)
			continue
		}
		if !modOK {
			continue
		}

		modKey := modAddr.Module[0].InstanceKey
		resKey := resAddr.Resource.Key
		if fmt.Sprintf("%T %v", modKey, modKey) != fmt.Sprintf("%T %v", resKey, resKey) {
			t.Errorf("key %q decodes as %T(%v) in a module step and %T(%v) in a resource segment; the two positions read one grammar and must give one answer",
				key, modKey, modKey, resKey, resKey)
		}
	}
}
