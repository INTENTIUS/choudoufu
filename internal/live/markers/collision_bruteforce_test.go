// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"fmt"
	"testing"
)

// TestAddressMatches_CrossGrammarCollision is the exact repro from issue
// #225: two ordinary for_each string keys in one resource block, neither
// containing anything exotic, where one key's current marker and the
// other key's Legacy marker collide byte for byte.
func TestAddressMatches_CrossGrammarCollision(t *testing.T) {
	addrA := `aws_instance.bar["a.b"]`
	addrB := `aws_instance.bar["a@db"]`

	observedA := EscapeAddress(addrA)
	if observedA != "aws_instance.bar:a@db" {
		t.Fatalf("EscapeAddress(A) = %q, want aws_instance.bar:a@db", observedA)
	}
	legacyB := LegacyEscapeAddress(addrB)
	if legacyB != "aws_instance.bar:a@db" {
		t.Fatalf("LegacyEscapeAddress(B) = %q, want aws_instance.bar:a@db", legacyB)
	}
	if observedA != legacyB {
		t.Fatalf("repro precondition broken: EscapeAddress(A)=%q != LegacyEscapeAddress(B)=%q", observedA, legacyB)
	}

	if !AddressMatches(observedA, addrA) {
		t.Errorf("AddressMatches(A's own marker, A) = false, want true")
	}
	if AddressMatches(observedA, addrB) {
		t.Errorf("AddressMatches(A's marker, B) = true, want false: A's canonical marker must not bind B")
	}
}

// TestAddressMatches_OldGrammarStillBindsOwnInstance is the property the
// fallback grammars exist for, pinned directly for each of them: a marker a
// prior run actually wrote under that grammar - not merely one that
// happens to collide with something else - still binds the instance it was
// written for. A regression here would strand every already-tagged
// resource whose key needed the grammar in question, which issue #225
// calls out as a worse outcome than the collision itself.
func TestAddressMatches_OldGrammarStillBindsOwnInstance(t *testing.T) {
	t.Run("legacy: at-sign key", func(t *testing.T) {
		declared := `aws_subnet.this["at@sign"]`
		observed := LegacyEscapeAddress(declared)
		if observed == EscapeAddress(declared) {
			t.Fatalf("test premise is wrong: legacy and current escaping agree (%q)", observed)
		}
		if !AddressMatches(observed, declared) {
			t.Errorf("AddressMatches(legacy marker, own address) = false, want true")
		}
	})

	t.Run("pre210: plus combined with dot", func(t *testing.T) {
		declared := `aws_subnet.this["a.b+c"]`
		observed := pre210EscapeAddress(declared)
		if observed == EscapeAddress(declared) || observed == LegacyEscapeAddress(declared) {
			t.Fatalf("test premise is wrong: pre210 escaping (%q) agrees with current or legacy", observed)
		}
		if !AddressMatches(observed, declared) {
			t.Errorf("AddressMatches(pre210 marker, own address) = false, want true")
		}
	})
}

// TestAddressMatches_DistinctOwnMarkersDoNotCross covers two resources that
// each carry their OWN genuine marker (no historical grammar involved on
// either side): neither's marker should ever match the other's declared
// address.
func TestAddressMatches_DistinctOwnMarkersDoNotCross(t *testing.T) {
	addrA := `aws_instance.bar["a.b"]`
	addrB := `aws_instance.bar["c.d"]`

	markerA := EscapeAddress(addrA)
	markerB := EscapeAddress(addrB)
	if markerA == markerB {
		t.Fatalf("test premise is wrong: A and B escape to the same marker (%q)", markerA)
	}

	if !AddressMatches(markerA, addrA) {
		t.Errorf("AddressMatches(A's marker, A) = false, want true")
	}
	if !AddressMatches(markerB, addrB) {
		t.Errorf("AddressMatches(B's marker, B) = false, want true")
	}
	if AddressMatches(markerA, addrB) {
		t.Errorf("AddressMatches(A's marker, B) = true, want false")
	}
	if AddressMatches(markerB, addrA) {
		t.Errorf("AddressMatches(B's marker, A) = true, want false")
	}
}

// bruteForceKeys enumerates every string over alphabet up to length maxLen,
// including the empty string.
func bruteForceKeys(alphabet []rune, maxLen int) []string {
	var out []string
	var rec func(prefix []rune, depth int)
	rec = func(prefix []rune, depth int) {
		out = append(out, string(prefix))
		if depth == maxLen {
			return
		}
		for _, r := range alphabet {
			rec(append(append([]rune{}, prefix...), r), depth+1)
		}
	}
	rec(nil, 0)
	return out
}

// TestAddressMatches_BruteForceCrossGrammarFalsePositives is the
// measurement issue #225 asks for: over every pair of distinct for_each
// string keys up to length 4 built from {a,b,.,@,:,+}, does any encoding
// of A's marker under any of the three grammars this package has ever
// stamped with wrongly match B's declared address?
//
// It is a regression gate: it must find zero. Run it before the fix lands
// (by reverting AddressMatches to the pre-#225 body) to see it fail with
// the same shape reported in the issue.
func TestAddressMatches_BruteForceCrossGrammarFalsePositives(t *testing.T) {
	alphabet := []rune{'a', 'b', '.', '@', ':', '+'}
	keys := bruteForceKeys(alphabet, 4)

	type finding struct {
		keyA, keyB string
		viaPre210  bool
		viaLegacy  bool
		observed   string
	}
	var findings []finding
	legacyOnly, pre210Only, both := 0, 0, 0

	// This mirrors the issue's own repro shape exactly: A is genuinely
	// tagged with ITS OWN real, current-grammar marker (nothing exotic,
	// nothing historical) - the question is whether some other declared
	// address B can claim that live resource anyway, through one of the
	// two fallback grammars AddressMatches also tries.
	for _, keyA := range keys {
		if keyA == "" {
			continue
		}
		addrA := fmt.Sprintf(`aws_instance.bar["%s"]`, keyA)
		observed := EscapeAddress(addrA)
		for _, keyB := range keys {
			if keyB == "" || keyB == keyA {
				continue
			}
			addrB := fmt.Sprintf(`aws_instance.bar["%s"]`, keyB)
			if observed == EscapeAddress(addrB) {
				// Two distinct keys escaping to the same canonical
				// marker is a different, pre-existing ambiguity
				// ([discovery]'s "one marker value for two declared
				// addresses" refusal) that this issue is not about;
				// exclude it so the count is only ever about a
				// fallback grammar overriding a canonical one.
				continue
			}
			viaPre210 := observed == pre210EscapeAddress(addrB)
			viaLegacy := observed == LegacyEscapeAddress(addrB)
			if !viaPre210 && !viaLegacy {
				continue
			}
			if AddressMatches(observed, addrB) {
				findings = append(findings, finding{keyA, keyB, viaPre210, viaLegacy, observed})
				switch {
				case viaPre210 && viaLegacy:
					both++
				case viaPre210:
					pre210Only++
				case viaLegacy:
					legacyOnly++
				}
			}
		}
	}

	t.Logf("brute force: %d keys up to length 4, %d cross-grammar false positives (legacy-only=%d, pre210-only=%d, both=%d)",
		len(keys), len(findings), legacyOnly, pre210Only, both)
	if len(findings) > 0 {
		limit := 20
		for i, f := range findings {
			if i >= limit {
				t.Logf("... and %d more", len(findings)-limit)
				break
			}
			t.Logf("FALSE POSITIVE: A=%q B=%q viaPre210=%v viaLegacy=%v observed=%q", f.keyA, f.keyB, f.viaPre210, f.viaLegacy, f.observed)
		}
		t.Errorf("%d cross-grammar false positives found (want 0)", len(findings))
	}
}
