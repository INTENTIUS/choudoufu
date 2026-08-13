// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"strings"
	"testing"
)

// TestContinuationTag_AddressTagKey pins the naming scheme issue #71
// introduces: TagAddress is chunk 0, tofu-address-2 is chunk 1, and so on.
func TestContinuationTag_AddressTagKey(t *testing.T) {
	if got := ContinuationTag(2); got != "tofu-address-2" {
		t.Errorf("ContinuationTag(2) = %q, want tofu-address-2", got)
	}
	if got := ContinuationTag(4); got != "tofu-address-4" {
		t.Errorf("ContinuationTag(4) = %q, want tofu-address-4", got)
	}

	cases := map[int]string{
		0: TagAddress,
		1: "tofu-address-2",
		2: "tofu-address-3",
		3: "tofu-address-4",
	}
	for i, want := range cases {
		if got := AddressTagKey(i); got != want {
			t.Errorf("AddressTagKey(%d) = %q, want %q", i, got, want)
		}
	}
}

// TestSplitAddress covers the boundary cases: a value that fits in one tag
// comes back unchanged (the pre-#71 shape, still the overwhelming common
// case), a value one character over the boundary splits into two chunks
// with the second holding exactly one character, and a value at the very
// top of the budget uses exactly MaxContinuations chunks.
func TestSplitAddress(t *testing.T) {
	t.Run("fits in one tag", func(t *testing.T) {
		short := "aws_vpc.main"
		got := SplitAddress(short)
		if len(got) != 1 || got[0] != short {
			t.Fatalf("SplitAddress(%q) = %v, want a single unchanged chunk", short, got)
		}
	})

	t.Run("exactly at the single-tag limit", func(t *testing.T) {
		exact := strings.Repeat("a", MaxTagValue)
		got := SplitAddress(exact)
		if len(got) != 1 || got[0] != exact {
			t.Fatalf("SplitAddress of an exactly-%d-char value split into %d chunks, want 1", MaxTagValue, len(got))
		}
	})

	t.Run("one character over splits in two", func(t *testing.T) {
		over := strings.Repeat("a", MaxTagValue+1)
		got := SplitAddress(over)
		if len(got) != 2 {
			t.Fatalf("SplitAddress of a %d-char value produced %d chunks, want 2", len(over), len(got))
		}
		if len(got[0]) != MaxTagValue {
			t.Errorf("chunk 0 is %d characters, want exactly %d", len(got[0]), MaxTagValue)
		}
		if got[1] != "a" {
			t.Errorf("chunk 1 = %q, want a single trailing character", got[1])
		}
	})

	t.Run("exactly the full budget uses every chunk", func(t *testing.T) {
		full := strings.Repeat("a", MaxAddressLen)
		got := SplitAddress(full)
		if len(got) != MaxContinuations {
			t.Fatalf("SplitAddress of a %d-char (MaxAddressLen) value produced %d chunks, want %d", MaxAddressLen, len(got), MaxContinuations)
		}
		for i, chunk := range got {
			if len(chunk) != MaxTagValue {
				t.Errorf("chunk %d is %d characters, want exactly %d", i, len(chunk), MaxTagValue)
			}
		}
		if strings.Join(got, "") != full {
			t.Errorf("chunks do not reassemble to the original value")
		}
	})

	t.Run("never returns more than MaxContinuations chunks", func(t *testing.T) {
		// Longer than the budget is a value lint's RuleOverlongAddress
		// already refuses to admit; SplitAddress still has to return
		// something bounded rather than growing without limit.
		absurd := strings.Repeat("a", MaxAddressLen*3)
		got := SplitAddress(absurd)
		if len(got) != MaxContinuations {
			t.Fatalf("SplitAddress of an absurdly long value produced %d chunks, want the cap of %d", len(got), MaxContinuations)
		}
	})
}

// TestGatherAddress_roundTrips is SplitAddress's read-side twin: whatever
// SplitAddress divided a value into, GatherAddress reads back as the one
// value it started from, keyed exactly the way [AddressTagKey] names each
// chunk.
func TestGatherAddress_roundTrips(t *testing.T) {
	for _, addr := range []string{
		"aws_vpc.main",
		strings.Repeat("a", MaxTagValue+1),
		strings.Repeat("a", MaxAddressLen),
	} {
		chunks := SplitAddress(addr)
		tags := make(map[string]string, len(chunks))
		for i, chunk := range chunks {
			tags[AddressTagKey(i)] = chunk
		}

		got, corrupt := GatherAddress(tags)
		if corrupt {
			t.Errorf("GatherAddress reported corrupt for a chain SplitAddress itself produced (%d chunks)", len(chunks))
		}
		if got != addr {
			t.Errorf("GatherAddress round trip: got %q, want %q", got, addr)
		}
	}
}

// TestGatherAddress_noTagAtAll is the ordinary "nothing here" case every
// caller already handled before continuation tags existed: an absent
// tofu-address is not corruption, just an absent marker.
func TestGatherAddress_noTagAtAll(t *testing.T) {
	got, corrupt := GatherAddress(map[string]string{"unrelated": "tag"})
	if corrupt {
		t.Error("an absent tofu-address was reported as a corrupt continuation chain")
	}
	if got != "" {
		t.Errorf("GatherAddress with no tofu-address = %q, want empty", got)
	}
}

// TestGatherAddress_gapIsCorrupt: a continuation tag present with an
// earlier one missing can only happen by hand-editing tags (deleting one
// continuation out of a set this package always writes as a whole), and it
// must be reported as corrupt rather than silently read as the address up
// to the gap.
func TestGatherAddress_gapIsCorrupt(t *testing.T) {
	cases := []struct {
		name string
		tags map[string]string
	}{
		{
			name: "tofu-address-2 missing, tofu-address-3 present",
			tags: map[string]string{
				TagAddress:         "aaa",
				ContinuationTag(3): "ccc",
			},
		},
		{
			name: "tofu-address-3 missing, tofu-address-4 present",
			tags: map[string]string{
				TagAddress:         "aaa",
				ContinuationTag(2): "bbb",
				ContinuationTag(4): "ddd",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, corrupt := GatherAddress(tc.tags)
			if !corrupt {
				t.Errorf("GatherAddress(%v) = %q, corrupt=false; want corrupt=true", tc.tags, got)
			}
		})
	}
}

// TestGatherAddress_completeChainNotCorrupt: every continuation tag present
// up to some n, and nothing beyond it, is exactly the shape SplitAddress
// writes and must never be flagged.
func TestGatherAddress_completeChainNotCorrupt(t *testing.T) {
	tags := map[string]string{
		TagAddress:         "aaa",
		ContinuationTag(2): "bbb",
		ContinuationTag(3): "ccc",
	}
	got, corrupt := GatherAddress(tags)
	if corrupt {
		t.Errorf("a complete three-chunk chain was reported as corrupt")
	}
	if got != "aaabbbccc" {
		t.Errorf("GatherAddress(%v) = %q, want %q", tags, got, "aaabbbccc")
	}
}

// TestValidMarkerAddress_wideBudget: the length bound moved from
// MaxTagValue to MaxAddressLen when continuation tags arrived, because every
// caller hands ValidMarkerAddress the logical address - declared or
// gathered - never one raw tag's worth alone.
func TestValidMarkerAddress_wideBudget(t *testing.T) {
	typeAndName := "aws_vpc."
	pastSingleTag := typeAndName + strings.Repeat("a", MaxTagValue+10)
	if !ValidMarkerAddress(pastSingleTag) {
		t.Errorf("an address past MaxTagValue but within MaxAddressLen was rejected")
	}

	atBudget := typeAndName + strings.Repeat("a", MaxAddressLen-len(typeAndName))
	if !ValidMarkerAddress(atBudget) {
		t.Errorf("an address exactly at MaxAddressLen was rejected")
	}

	overBudget := typeAndName + strings.Repeat("a", MaxAddressLen-len(typeAndName)+1)
	if ValidMarkerAddress(overBudget) {
		t.Errorf("an address one character past MaxAddressLen was accepted")
	}
}
