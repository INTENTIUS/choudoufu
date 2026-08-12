// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"strings"
	"testing"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/stateless/discovery"
)

// The rename pass runs against the same P0.1 estate fixture the rest of this
// package's tests use, so the resource blocks are real ones: aws_subnet.this
// is a for_each block, aws_eip.pool is a count block, and the difference
// between them is the difference these tests are about.
//
// The discovery results are hand-written because they are the input this pass
// takes: the orphans and unbound instances a run would arrive with after a
// for_each key was edited in configuration.

// TestRenameCandidate is the whole point: one live resource marked with a key
// the configuration no longer declares, one declared instance nothing
// claimed, same block - offered as a rename, with the command that performs
// it.
func TestRenameCandidate(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
		Orphans: []discovery.OwnedResource{orphan("aws_subnet", "subnet-xyz", "private-a", "aws_subnet.this:a")},
	})

	if len(res.Renames) != 1 {
		t.Fatalf("want exactly one rename candidate, got:\n%s", res)
	}
	c := res.Renames[0]
	if got := c.Old.String(); got != `aws_subnet.this["a"]` {
		t.Errorf("old address is %q, want the address the marker claims, unescaped", got)
	}
	if got := c.New.String(); got != `aws_subnet.this["c"]` {
		t.Errorf("new address is %q, want the declared instance nothing claimed", got)
	}
	if c.LiveID != "subnet-xyz" || c.TypeName != "aws_subnet" {
		t.Errorf("the candidate does not carry the live resource: %s", c)
	}
	if c.Block != "aws_subnet.this" {
		t.Errorf("the candidate names block %q", c.Block)
	}
	if c.Normalized != "aws_subnet.this:a" {
		t.Errorf("the candidate does not carry the marker it matched on: %q", c.Normalized)
	}

	want := `choudoufu live-mv 'aws_subnet.this["a"]' 'aws_subnet.this["c"]'`
	if c.Command != want {
		t.Errorf("command is\n  %s\nwant\n  %s", c.Command, want)
	}
	if len(res.Ambiguous) != 0 {
		t.Errorf("a one-to-one pairing was also reported as ambiguous:\n%s", res)
	}

	if got, ok := res.RenameFor(mustAddr(t, `aws_subnet.this["c"]`)); !ok || got.LiveID != "subnet-xyz" {
		t.Errorf("RenameFor does not find the candidate by its destination:\n%s", res)
	}
}

// TestRenameCandidateFromAnUnescapedMarker: a pre-spec marker carrying the
// address unescaped binds the same way, because the comparison is always made
// on the escaped form.
func TestRenameCandidateFromAnUnescapedMarker(t *testing.T) {
	o := orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a")
	o.Marker = `aws_subnet.this["a"]`

	res := classifyFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
		Orphans: []discovery.OwnedResource{o},
	})

	if len(res.Renames) != 1 {
		t.Fatalf("want one rename candidate, got:\n%s", res)
	}
	if got := res.Renames[0].Old.String(); got != `aws_subnet.this["a"]` {
		t.Errorf("old address is %q", got)
	}
}

// TestRenameAmbiguousTwoOrphans: two live resources at keys that are gone and
// one declared instance waiting. Something moved; which one is not something
// a marker says, so nothing is offered and no command is printed.
func TestRenameAmbiguousTwoOrphans(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_subnet", 2)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
		Orphans: []discovery.OwnedResource{
			orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a"),
			orphan("aws_subnet", "subnet-abc", "", "aws_subnet.this:b"),
		},
	})

	if len(res.Renames) != 0 {
		t.Fatalf("a rename was offered with two live candidates for it:\n%s", res)
	}
	amb, ok := res.AmbiguousFor("aws_subnet.this")
	if !ok {
		t.Fatalf("the block was not reported as ambiguous:\n%s", res)
	}
	if len(amb.Live) != 2 || len(amb.Declared) != 1 {
		t.Fatalf("the ambiguity does not name both sides: %s", amb)
	}
	if amb.Live[0].LiveID != "subnet-xyz" || amb.Live[1].LiveID != "subnet-abc" {
		t.Errorf("the ambiguity names %v, want both live resources in key order", amb.Live)
	}
	if amb.Declared[0] != `aws_subnet.this["c"]` {
		t.Errorf("the ambiguity names declared %v", amb.Declared)
	}
	if amb.Detail == "" {
		t.Error("an ambiguity was reported with no explanation")
	}
	if strings.Contains(res.String(), "live-mv") {
		t.Errorf("an ambiguous block still printed a command:\n%s", res)
	}
}

// TestRenameAmbiguousTwoUnbound is the same rule from the other side: one
// orphan and two declared instances waiting is equally unanswerable.
func TestRenameAmbiguousTwoUnbound(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans: []discovery.TypeScan{scan("aws_subnet", 1)},
		Unbound: []addrs.AbsResourceInstance{
			mustAddr(t, `aws_subnet.this["c"]`),
			mustAddr(t, `aws_subnet.this["d"]`),
		},
		Orphans: []discovery.OwnedResource{orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a")},
	})

	if len(res.Renames) != 0 {
		t.Fatalf("a rename was offered with two destinations for it:\n%s", res)
	}
	amb, ok := res.AmbiguousFor("aws_subnet.this")
	if !ok {
		t.Fatalf("the block was not reported as ambiguous:\n%s", res)
	}
	if len(amb.Declared) != 2 {
		t.Errorf("the ambiguity names %v, want both declared instances", amb.Declared)
	}
}

// TestRenameNeverCrossesBlocks: the block match is the pairing, so a marker
// naming another block of the same type - or a resource of another type
// entirely - pairs with nothing.
func TestRenameNeverCrossesBlocks(t *testing.T) {
	t.Run("another block", func(t *testing.T) {
		res := classifyFixture(t, discovery.Result{
			Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
			Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
			Orphans: []discovery.OwnedResource{orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.other:a")},
		})
		if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
			t.Errorf("a marker naming another block was paired:\n%s", res)
		}
	})

	t.Run("another type", func(t *testing.T) {
		// A live resource of a different type carrying the subnet block's
		// marker is a broken record, not a rename: the block match has to
		// hold on the type as well as on the address.
		res := classifyFixture(t, discovery.Result{
			Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
			Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
			Orphans: []discovery.OwnedResource{orphan("aws_route_table", "rtb-xyz", "", "aws_subnet.this:a")},
		})
		if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
			t.Errorf("a marker on a resource of another type was paired:\n%s", res)
		}
	})
}

// TestRenameExcludesCount: a count set's live members are matched as a set by
// slot, and a member past the declared count is surplus. Offering to rename
// one would fight the scale-down rule, so the count block never appears here
// - from either side.
func TestRenameExcludesCount(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_eip", 1)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, "aws_eip.pool[1]")},
		Orphans: []discovery.OwnedResource{orphan("aws_eip", "eipalloc-c", "", "aws_eip.pool:7")},
	})
	if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
		t.Errorf("a count member was offered as a rename:\n%s", res)
	}
}

// TestRenameNothingToPair: an orphan with no unbound instance beside it is an
// ordinary removal, and an unbound instance with no orphan beside it is an
// ordinary create. Neither is a rename, and neither produces a line.
func TestRenameNothingToPair(t *testing.T) {
	t.Run("orphan only", func(t *testing.T) {
		res := classifyFixture(t, discovery.Result{
			Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
			Orphans: []discovery.OwnedResource{orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a")},
		})
		if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
			t.Errorf("an orphan with nothing to pair with produced a rename:\n%s", res)
		}
	})

	t.Run("unbound only", func(t *testing.T) {
		res := classifyFixture(t, discovery.Result{
			Scans:   []discovery.TypeScan{scan("aws_subnet", 0)},
			Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
		})
		if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
			t.Errorf("an unbound instance with nothing to pair with produced a rename:\n%s", res)
		}
	})

	t.Run("neither", func(t *testing.T) {
		res := classifyFixture(t, discovery.Result{Scans: []discovery.TypeScan{scan("aws_subnet", 0)}})
		if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
			t.Errorf("an empty discovery result produced a rename:\n%s", res)
		}
	})
}

// TestRenameKeyedByNoKeyInstance: a block with no instance key cannot have
// had a key renamed. The unbound aws_vpc.main below is exactly the case the
// bind-candidate path handles, and this pass leaves it alone.
func TestRenameKeyedByNoKeyInstance(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_vpc", 1)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, "aws_vpc.main")},
		Orphans: []discovery.OwnedResource{orphan("aws_vpc", "vpc-old", "", "aws_vpc.retired")},
	})
	if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
		t.Errorf("an unkeyed instance produced a rename:\n%s", res)
	}
}

// TestRenameUnreadableKey: the escaping rule cannot round-trip a key
// containing '.' or ':', so there is no address to write into a command.
// Being unable to name it does not make it disappear - the block is reported
// as ambiguous rather than silently skipped, because a resource that is there
// and cannot be paired is exactly what the operator needs told.
func TestRenameUnreadableKey(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
		Orphans: []discovery.OwnedResource{orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a.b")},
	})

	if len(res.Renames) != 0 {
		t.Fatalf("an undecodable key was turned into a command anyway:\n%s", res)
	}
	amb, ok := res.AmbiguousFor("aws_subnet.this")
	if !ok {
		t.Fatalf("the undecodable marker was dropped instead of reported:\n%s", res)
	}
	if !strings.Contains(amb.Detail, "MARKERS.md") {
		t.Errorf("the detail does not say why the key cannot be read: %q", amb.Detail)
	}
}

// TestRenameSameKeyIsNotOffered: a marker and a declared instance agreeing on
// their key would have bound in discovery. Arriving here means something
// rewrote a marker underneath the run, and the answer is to say so rather
// than to emit a rename from an address to itself.
func TestRenameSameKeyIsNotOffered(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["a"]`)},
		Orphans: []discovery.OwnedResource{orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a")},
	})
	if len(res.Renames) != 0 {
		t.Fatalf("a rename from an address to itself was offered:\n%s", res)
	}
	if _, ok := res.AmbiguousFor("aws_subnet.this"); !ok {
		t.Errorf("the disagreement was not reported:\n%s", res)
	}
}

// TestRenameCommandQuoting: the command is printed to be pasted, so both
// addresses are quoted whole - the brackets and the double quotes inside them
// are shell metacharacters otherwise.
func TestRenameCommandQuoting(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{`aws_subnet.this["a"]`, `'aws_subnet.this["a"]'`},
		{`aws_subnet.this["it's"]`, `'aws_subnet.this["it'\''s"]'`},
	} {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// orphan is one live resource carrying this estate's marker at an address no
// declared instance matches, as discovery reports it.
func orphan(typeName, id, displayName, marker string) discovery.OwnedResource {
	return discovery.OwnedResource{
		TypeName:    typeName,
		ImportID:    id,
		Marker:      marker,
		Normalized:  discovery.EscapeAddress(marker),
		DisplayName: displayName,
		Tags: map[string]string{
			discovery.TagEstate:  estateName,
			discovery.TagAddress: marker,
		},
	}
}
