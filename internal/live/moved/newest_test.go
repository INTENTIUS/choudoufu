// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package moved

import (
	"path/filepath"
	"testing"
)

// TestNewestFollowsChains is [Newest] run over the same fixture
// TestOriginsFollowsChains uses, in the opposite direction: given the
// OLDEST address in a two-hop rename chain (a to b to c, both hops still
// in the source because they came from a published module upgrade), it
// must land on c - the address [Origins] would have to be asked about to
// get "a" back out. This is the exact shape
// gauntlet:corpus-security-group-complete/day2_remove needed:
// [projection.RecordStore] never rewrites a record-store key for a bare
// HCL `moved` block (only live-mv's MoveRecord does), so a key decoded
// straight off the store still names the OLDEST address in the chain, and
// discovery/recordorphan_read.go's own leg has nothing else that would
// translate it forward to the address a destroy actually has to be
// proposed under.
func TestNewestFollowsChains(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join("testdata", "chain"))
	stmts := Honoured(cfg)

	tests := []struct {
		name string
		from string
		want string
	}{
		{name: "two hops from the oldest address", from: "aws_s3_bucket.a", want: "aws_s3_bucket.c"},
		{name: "one hop from the middle address", from: "aws_s3_bucket.b", want: "aws_s3_bucket.c"},
		{name: "already at the newest address", from: "aws_s3_bucket.c", want: "aws_s3_bucket.c"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := Newest(stmts, mustAddr(t, test.from))
			if !ok {
				t.Fatalf("Newest(%s) reported ok=false, want a clean resolution to %s", test.from, test.want)
			}
			if got.String() != mustAddr(t, test.want).String() {
				t.Errorf("Newest(%s) = %s, want %s", test.from, got, test.want)
			}
		})
	}
}

// TestNewestLeavesAnUntouchedAddressAlone: an address no honoured statement
// ever names as a `from` endpoint - the ordinary case for every record-store
// key this leg reads that was never part of a rename - is returned exactly
// as it arrived, with ok true. Nothing here has to special-case "no
// statements at all" separately: an empty stmts slice takes the same
// zero-match path as a nonempty one that simply never matches.
func TestNewestLeavesAnUntouchedAddressAlone(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join("testdata", "chain"))
	stmts := Honoured(cfg)

	addr := mustAddr(t, "aws_s3_bucket.untouched")
	got, ok := Newest(stmts, addr)
	if !ok || got.String() != addr.String() {
		t.Errorf("Newest(%s) = %s, ok=%v, want it unchanged with ok=true", addr, got, ok)
	}

	got, ok = Newest(nil, addr)
	if !ok || got.String() != addr.String() {
		t.Errorf("Newest(%s) with no statements = %s, ok=%v, want it unchanged with ok=true", addr, got, ok)
	}
}

// TestNewestRefusesAnAmbiguousFork: two honoured statements both claim
// aws_s3_bucket.ambiguous as their `from` endpoint, naming two different
// destinations. Preferring either would be a guess this package's "never
// approximated" discipline (see [Origins]'s own doc comment on
// maxOrigins) refuses to make, so Newest reports ok=false and hands back
// the address it was given - the caller (recordorphan_read.go) keeps its
// pre-fix behaviour for exactly this case, rather than silently destroying
// under a re-keyed address neither statement can be trusted to name.
func TestNewestRefusesAnAmbiguousFork(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join("testdata", "fork"))
	stmts := Honoured(cfg)
	if len(stmts) != 2 {
		t.Fatalf("the fork fixture's own moved blocks did not both come back honoured: %s", statementStrings(stmts))
	}

	addr := mustAddr(t, "aws_s3_bucket.ambiguous")
	got, ok := Newest(stmts, addr)
	if ok {
		t.Fatalf("Newest(%s) resolved to %s despite two statements disagreeing about where it went - it must refuse rather than pick one", addr, got)
	}
	if got.String() != addr.String() {
		t.Errorf("Newest(%s) on refusal returned %s, want the address unchanged", addr, got)
	}
}
