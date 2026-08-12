// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

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

// ---------------------------------------------------------------------------
// Content as a tiebreaker
// ---------------------------------------------------------------------------
//
// The estate fixture's subnet block reads both of its identity-bearing
// arguments from each.value, so every pairing above was decided by the block
// match alone. The tests below run against a block that fixes
// availability_zone as a literal, which is what gives content something to
// say - and they pin the exact extent of what it may say: it can remove a
// pairing and it can settle an ambiguous block, but it can never create a
// pairing the block match refused.

// TestRenameContentIsOptional: a one-to-one block match is offered exactly as
// it always was, whether the provider sent no object at all or an object that
// agrees. Content absent is not content against.
func TestRenameContentIsOptional(t *testing.T) {
	t.Run("no object", func(t *testing.T) {
		res := classifyContentFixture(t, discovery.Result{
			Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
			Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
			Orphans: []discovery.OwnedResource{orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a")},
		})
		if len(res.Renames) != 1 {
			t.Fatalf("want one rename candidate with no content to compare, got:\n%s", res)
		}
		if len(res.Renames[0].MatchedOn) != 0 {
			t.Errorf("a pairing decided by the block match alone claims content evidence: %v", res.Renames[0].MatchedOn)
		}
	})

	t.Run("an agreeing object", func(t *testing.T) {
		res := classifyContentFixture(t, discovery.Result{
			Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
			Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
			Orphans: []discovery.OwnedResource{
				withContent(orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a"),
					map[string]string{"availability_zone": "us-east-1a"}),
			},
		})
		if len(res.Renames) != 1 {
			t.Fatalf("want one rename candidate, got:\n%s", res)
		}
		c := res.Renames[0]
		if len(c.MatchedOn) != 1 || c.MatchedOn[0] != (AttrMatch{Attr: "availability_zone", Value: "us-east-1a"}) {
			t.Errorf("the agreement was not recorded as evidence: %v", c.MatchedOn)
		}
	})
}

// TestRenameContentResolvesAmbiguity: two live resources at keys that are
// gone, one declared instance waiting - unanswerable by the markers alone,
// and exactly the case the live objects can settle when the block fixes an
// identity-bearing argument. One orphan carries the declared availability
// zone and one does not, so a single pairing survives, agrees positively, and
// is offered - with the argument it was decided on recorded on the candidate.
func TestRenameContentResolvesAmbiguity(t *testing.T) {
	res := classifyContentFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_subnet", 2)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
		Orphans: []discovery.OwnedResource{
			withContent(orphan("aws_subnet", "subnet-keep", "", "aws_subnet.this:a"),
				map[string]string{"availability_zone": "us-east-1a"}),
			withContent(orphan("aws_subnet", "subnet-else", "", "aws_subnet.this:b"),
				map[string]string{"availability_zone": "us-east-1b"}),
		},
	})

	if len(res.Renames) != 1 {
		t.Fatalf("content left one agreeing pairing standing and it was not offered:\n%s", res)
	}
	c := res.Renames[0]
	if c.LiveID != "subnet-keep" {
		t.Errorf("the offered pairing names live %s, want the orphan whose content agrees", c.LiveID)
	}
	if c.Old.String() != `aws_subnet.this["a"]` || c.New.String() != `aws_subnet.this["c"]` {
		t.Errorf("the pairing is %s -> %s", c.Old, c.New)
	}
	if len(c.MatchedOn) != 1 || c.MatchedOn[0] != (AttrMatch{Attr: "availability_zone", Value: "us-east-1a"}) {
		t.Errorf("the candidate does not carry the evidence it was decided on: %v", c.MatchedOn)
	}
	if len(res.Ambiguous) != 0 {
		t.Errorf("a block content settled is still reported ambiguous:\n%s", res)
	}
}

// TestRenameContentStaysAmbiguous: content that cannot settle a block leaves
// it exactly where the block match left it - reported, with no command.
func TestRenameContentStaysAmbiguous(t *testing.T) {
	t.Run("two declared instances share one body", func(t *testing.T) {
		// One orphan agreeing with the block's configuration says nothing
		// about which of two unclaimed keys it became: a for_each block has
		// one body, so content cannot tell c from d.
		res := classifyContentFixture(t, discovery.Result{
			Scans: []discovery.TypeScan{scan("aws_subnet", 1)},
			Unbound: []addrs.AbsResourceInstance{
				mustAddr(t, `aws_subnet.this["c"]`),
				mustAddr(t, `aws_subnet.this["d"]`),
			},
			Orphans: []discovery.OwnedResource{
				withContent(orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a"),
					map[string]string{"availability_zone": "us-east-1a"}),
			},
		})
		if len(res.Renames) != 0 {
			t.Fatalf("content picked one of two declared instances that share a body:\n%s", res)
		}
		if _, ok := res.AmbiguousFor("aws_subnet.this"); !ok {
			t.Fatalf("the unsettled block was not reported:\n%s", res)
		}
	})

	t.Run("the survivor cannot be compared", func(t *testing.T) {
		// Content ruled one orphan out, but the one left sent no object.
		// "Everything else was ruled out" is not positive agreement, so
		// nothing is offered.
		res := classifyContentFixture(t, discovery.Result{
			Scans:   []discovery.TypeScan{scan("aws_subnet", 2)},
			Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
			Orphans: []discovery.OwnedResource{
				orphan("aws_subnet", "subnet-mute", "", "aws_subnet.this:a"),
				withContent(orphan("aws_subnet", "subnet-else", "", "aws_subnet.this:b"),
					map[string]string{"availability_zone": "us-east-1b"}),
			},
		})
		if len(res.Renames) != 0 {
			t.Fatalf("a pairing that could not be compared was offered on elimination alone:\n%s", res)
		}
		if _, ok := res.AmbiguousFor("aws_subnet.this"); !ok {
			t.Fatalf("the unsettled block was not reported:\n%s", res)
		}
	})
}

// TestRenameContentRemovesTheOnlyPairing: one of each, block-matched, but the
// live object's availability zone is not the declared one. A resource whose
// identifying arguments disagree is not the declared instance under a new
// key, so the pairing is removed and the report shows the comparison.
func TestRenameContentRemovesTheOnlyPairing(t *testing.T) {
	res := classifyContentFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
		Orphans: []discovery.OwnedResource{
			withContent(orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a"),
				map[string]string{"availability_zone": "us-east-1b"}),
		},
	})

	if len(res.Renames) != 0 {
		t.Fatalf("a pairing content disqualified was offered anyway:\n%s", res)
	}
	amb, ok := res.AmbiguousFor("aws_subnet.this")
	if !ok {
		t.Fatalf("the disqualified pairing was dropped instead of reported:\n%s", res)
	}
	if !strings.Contains(amb.Detail, "availability_zone") ||
		!strings.Contains(amb.Detail, `"us-east-1b"`) || !strings.Contains(amb.Detail, `"us-east-1a"`) {
		t.Errorf("the detail does not show the values that disagreed: %q", amb.Detail)
	}
}

// TestRenameContentNeverOverridesTheBlockMatch: agreement on content is not a
// pairing. A live resource of another type, or marked for another block,
// stays unpaired no matter what its attributes say - the block match is the
// requirement, and content only ever narrows what it admits.
func TestRenameContentNeverOverridesTheBlockMatch(t *testing.T) {
	agreeing := map[string]string{"availability_zone": "us-east-1a"}

	t.Run("another type", func(t *testing.T) {
		res := classifyContentFixture(t, discovery.Result{
			Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
			Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
			Orphans: []discovery.OwnedResource{
				withContent(orphan("aws_route_table", "rtb-xyz", "", "aws_subnet.this:a"), agreeing),
			},
		})
		if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
			t.Errorf("agreeing content paired a resource of another type:\n%s", res)
		}
	})

	t.Run("another block", func(t *testing.T) {
		res := classifyContentFixture(t, discovery.Result{
			Scans:   []discovery.TypeScan{scan("aws_subnet", 1)},
			Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)},
			Orphans: []discovery.OwnedResource{
				withContent(orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.other:a"), agreeing),
			},
		})
		if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
			t.Errorf("agreeing content paired a marker naming another block:\n%s", res)
		}
	})
}

// classifyContentFixture classifies against a one-block configuration whose
// availability_zone is a literal, so that a content comparison has a
// configuration value to read. cidr_block stays on each.value, as it would be
// in any real for_each block, pinning that an unreadable argument is skipped
// rather than treated as a mismatch.
func classifyContentFixture(t *testing.T, res discovery.Result) *Result {
	t.Helper()

	dir := t.TempDir()
	cfg := `resource "aws_subnet" "this" {
  for_each          = { c = "10.42.3.0/24", d = "10.42.4.0/24" }
  cidr_block        = each.value
  availability_zone = "us-east-1a"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("writing the content fixture: %v", err)
	}

	out, diags := Classify(context.Background(), Request{
		Estate:    estateName,
		Config:    loadConfig(t, dir),
		Discovery: &res,
	})
	if diags.HasErrors() {
		t.Fatalf("classification failed:\n%s", renderDiags(diags))
	}
	return out
}

// withContent attaches a listed object to an orphan, the way discovery does
// when the provider sends full resource objects.
func withContent(o discovery.OwnedResource, attrs map[string]string) discovery.OwnedResource {
	vals := make(map[string]cty.Value, len(attrs))
	for k, v := range attrs {
		vals[k] = cty.StringVal(v)
	}
	o.Resource = cty.ObjectVal(vals)
	return o
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
