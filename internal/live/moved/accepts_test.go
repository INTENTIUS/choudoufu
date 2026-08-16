// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package moved

import (
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// [Accepts] is the single definition of "this tofu-address marker names this
// instance" (GitHub issue #244). internal/live/projection asks it directly and
// internal/live/discovery builds its marker index from [Aliases], which it is
// written in terms of, so a disagreement between the two layers about what an
// address marker means has one place to be wrong rather than two - which is
// what #244 was.

// TestAcceptsTakesTheDeclaredAddressAndItsOrigins walks the same estate
// TestOriginsCoversEveryCorpusShape does, from the marker side: for each
// declared instance, the marker naming it is accepted, every origin's marker
// is accepted, and a marker naming a resource the moved blocks never mention
// is not.
func TestAcceptsTakesTheDeclaredAddressAndItsOrigins(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join("testdata", "estate"))
	stmts := Honoured(cfg)

	for _, declared := range []string{
		"aws_s3_bucket.new",
		"aws_s3_bucket_versioning.this[0]",
		"module.queues.aws_sqs_queue.doi",
	} {
		addr := mustAddr(t, declared)

		if !Accepts(stmts, addr, markers.EscapeAddress(declared)) {
			t.Errorf("Accepts(%s, its own address) = false; a marker naming the instance itself must always be accepted", declared)
		}
		for _, origin := range Aliases(stmts, addr) {
			if !Accepts(stmts, addr, markers.EscapeAddress(origin.String())) {
				t.Errorf("Accepts(%s, %q) = false, but it is one of the instance's own origins - a moved block would not carry through", declared, origin)
			}
		}
		// A resource of the right type that no moved block in this estate
		// mentions. Nothing may widen the acceptable set to it.
		if Accepts(stmts, addr, markers.EscapeAddress("aws_s3_bucket.nowhere_near_this")) {
			t.Errorf("Accepts(%s, an unrelated address) = true; the alias set is the moved blocks' origins, not \"any address\"", declared)
		}
		if Accepts(stmts, addr, "") {
			t.Errorf("Accepts(%s, \"\") = true; an absent marker names nothing, and the caller has to decide what that means", declared)
		}
	}
}

// TestAcceptsWithNoStatementsIsPlainAddressEquality: the overwhelmingly
// common case. A configuration with no moved blocks must cost one comparison
// and must behave exactly as a direct [markers.AddressMatches] would.
func TestAcceptsWithNoStatementsIsPlainAddressEquality(t *testing.T) {
	addr := mustAddr(t, `aws_s3_bucket.new`)

	if !Accepts(nil, addr, markers.EscapeAddress("aws_s3_bucket.new")) {
		t.Error("its own marker was refused with no moved blocks in play")
	}
	if Accepts(nil, addr, markers.EscapeAddress("aws_s3_bucket.old")) {
		t.Error("a foreign address was accepted with no moved blocks to justify it")
	}
	if got := Aliases(nil, addr); len(got) != 0 {
		t.Errorf("Aliases with no statements = %v, want none", got)
	}
}

// TestAcceptsHonoursOlderEscapingGrammars is why this compares through
// [markers.AddressMatches] and not [markers.EscapeAddress] equality. A marker
// a prior run wrote under the pre-issue-#178 grammar for a for_each key
// containing "@" still names the instance it always named; a bare comparison
// reads it as unowned, which is the cross-grammar hole that produced 107 false
// positives earlier in this campaign.
func TestAcceptsHonoursOlderEscapingGrammars(t *testing.T) {
	// "a@x", not "a@db": the pre-#178 escaping of a key containing "@d"
	// coincides with the CURRENT escaping of a completely different key
	// ("a.b"), and markers.AddressMatches deliberately lets the canonical
	// reading win there (issue #225). This key has no such twin.
	const declared = `aws_s3_bucket.new["a@x"]`
	addr := mustAddr(t, declared)

	legacy := markers.LegacyEscapeAddress(declared)
	if legacy == markers.EscapeAddress(declared) {
		t.Fatalf("this fixture no longer exercises a grammar difference: both escapings are %q", legacy)
	}
	if !Accepts(nil, addr, legacy) {
		t.Errorf("a pre-#178 marker %q was refused for %s", legacy, declared)
	}
}
