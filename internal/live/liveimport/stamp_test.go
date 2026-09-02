// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is issue #71's own gap, closed: the commit that introduced
// tofu-address continuation tags (84a12e089) shipped with a note that
// internal/live/liveimport had no unit tests of its own for the new
// split-write path, only ratify_test.go's fixture-driven coverage of the
// single-tag shape that predates it. approveOne is unexported and has no
// seam of its own to test through besides calling it directly, the same way
// this package's own tests reach [taggable] and [tagsFromObject] in tags.go
// - see that file's package comment.
//
// vpcSchema, capturingProvider and vpcEligible below are this file's own
// minimal harness, deliberately smaller than ratify_test.go's fixture-wide
// fakeCloud: every test here is about one resource's write, not about
// Ratify's classification, so there is no tfstate fixture or ConfiguredProvider
// seam to wire up.

// vpcSchema is a minimal taggable schema: an id and a tags map, enough for
// [tagsFromObject] and [withTags] to do their jobs and nothing else to get
// in the way of what these tests are about.
func vpcSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":   {Type: cty.String, Computed: true},
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}}}
}

// capturingProvider is a [tofu.MockProvider] wired for approveOne: every
// plan is accepted as proposed (no replace, nothing else changed) and every
// apply is recorded rather than sent anywhere, so a test can assert on
// exactly the tag set a write would have landed.
type capturingProvider struct {
	*tofu.MockProvider
	applyCount  int
	appliedTags map[string]string
}

func newCapturingProvider() *capturingProvider {
	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	c := &capturingProvider{MockProvider: p}

	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	p.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		c.applyCount++
		tags := map[string]string{}
		tagsVal := r.PlannedState.GetAttr("tags")
		if !tagsVal.IsNull() {
			for it := tagsVal.ElementIterator(); it.Next(); {
				k, v := it.Element()
				tags[k.AsString()] = v.AsString()
			}
		}
		c.appliedTags = tags
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return c
}

// vpcEligible builds an *eligible carrying a live aws_vpc object whose tags
// attribute is exactly liveTags, backed by a fresh capturingProvider.
func vpcEligible(liveTags map[string]string) (*eligible, *capturingProvider) {
	p := newCapturingProvider()

	tagVals := make(map[string]cty.Value, len(liveTags))
	for k, v := range liveTags {
		tagVals[k] = cty.StringVal(v)
	}
	var tagsVal cty.Value
	if len(tagVals) == 0 {
		tagsVal = cty.MapValEmpty(cty.String)
	} else {
		tagsVal = cty.MapVal(tagVals)
	}

	e := &eligible{residuable{
		provider: p,
		schema:   vpcSchema(),
		typeName: "aws_vpc",
		applied: cty.ObjectVal(map[string]cty.Value{
			"id":   cty.StringVal("vpc-x"),
			"tags": tagsVal,
		}),
		identity: cty.NilVal,
	}}
	return e, p
}

func mustAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("bad test address %q: %s", s, diags.Err())
	}
	return addr
}

// longAddr and its chunks: a resource name long enough that its escaped
// address is 308 characters, past MaxTagValue (256) but comfortably inside
// MaxAddressLen (1024) - the smallest interesting case, needing exactly two
// tags rather than the whole four-tag budget SplitAddress's own tests in
// internal/live/markers already cover.
const longName = "aws_vpc." // + 300 x's below
var longAddrOnce = strings.Repeat("x", 300)

func longAddr(t *testing.T) (addrs.AbsResourceInstance, []string) {
	t.Helper()
	addr := mustAddr(t, longName+longAddrOnce)
	escaped := discovery.EscapeAddress(addr.String())
	chunks := discovery.SplitAddress(escaped)
	if len(chunks) != 2 {
		t.Fatalf("test setup: escaped address is %d characters and split into %d chunks, want 2 (adjust the fixture)", len(escaped), len(chunks))
	}
	return addr, chunks
}

// ---------------------------------------------------------------------------
// The write path: a fresh adoption whose address needs continuation tags
// ---------------------------------------------------------------------------

// TestApproveOne_SplitsLongAddressAcrossContinuationTags is the positive
// case the 84a12e089 commit message calls out as untested: a resource with
// no existing markers, whose declared address does not fit in one tag,
// gets stamped with tofu-address holding SplitAddress's first chunk and
// tofu-address-2 holding the rest - not truncated, not refused (refusal is
// lint's RuleOverlongAddress's job, at admission time, before Approve ever
// sees the resource).
func TestApproveOne_SplitsLongAddressAcrossContinuationTags(t *testing.T) {
	addr, chunks := longAddr(t)
	e, p := vpcEligible(nil) // no existing tags: a brand new adoption

	out := approveOne(context.Background(), "acme", addr, e, "")

	if out.Outcome != OutcomeStamped {
		t.Fatalf("Outcome = %s, want STAMPED (detail: %s)", out.Outcome, out.Detail)
	}
	if p.applyCount != 1 {
		t.Fatalf("ApplyResourceChange was called %d times, want 1", p.applyCount)
	}
	if got := p.appliedTags[discovery.TagEstate]; got != "acme" {
		t.Errorf("tofu-estate = %q, want acme", got)
	}
	if got := p.appliedTags[discovery.TagAddress]; got != chunks[0] {
		t.Errorf("tofu-address = %q, want chunk 0 %q", got, chunks[0])
	}
	if got := p.appliedTags[discovery.ContinuationTag(2)]; got != chunks[1] {
		t.Errorf("tofu-address-2 = %q, want chunk 1 %q", got, chunks[1])
	}
	if _, present := p.appliedTags[discovery.ContinuationTag(3)]; present {
		t.Errorf("tofu-address-3 was written for a two-chunk address; only chunks SplitAddress actually produced must be written")
	}
	// GatherAddress on what was just written round-trips to the address
	// that was asked for - the write-side and read-side agree with each
	// other, not just with SplitAddress in isolation.
	gathered, corrupt := discovery.GatherAddress(p.appliedTags)
	if corrupt {
		t.Fatalf("the tags approveOne wrote read back as a corrupt continuation chain: %v", p.appliedTags)
	}
	if want := discovery.EscapeAddress(addr.String()); gathered != want {
		t.Errorf("GatherAddress on the written tags = %q, want %q", gathered, want)
	}
}

// ---------------------------------------------------------------------------
// Idempotency: a second run over an already multi-tag-stamped resource
// ---------------------------------------------------------------------------

// TestApproveOne_AlreadySplitStampedIsIdempotent mirrors
// ratify_test.go's TestApprove_secondRunIsIdempotent for the split-address
// shape specifically: a resource already carrying this estate's markers
// across tofu-address and tofu-address-2, naming exactly the address this
// run would write, is a no-op - the same "a second live-import run is
// idempotent" promise the single-tag path already had, extended to the new
// tag shape rather than broken by it.
func TestApproveOne_AlreadySplitStampedIsIdempotent(t *testing.T) {
	addr, chunks := longAddr(t)
	e, p := vpcEligible(map[string]string{
		discovery.TagEstate:          "acme",
		discovery.TagAddress:         chunks[0],
		discovery.ContinuationTag(2): chunks[1],
	})

	out := approveOne(context.Background(), "acme", addr, e, "")

	if out.Outcome != OutcomeAlreadyStamped {
		t.Fatalf("Outcome = %s, want ALREADY_STAMPED (detail: %s)", out.Outcome, out.Detail)
	}
	if p.applyCount != 0 {
		t.Errorf("ApplyResourceChange was called %d times, want 0 for an already-stamped resource", p.applyCount)
	}
}

// ---------------------------------------------------------------------------
// Conflict: an existing split marker naming a different address
// ---------------------------------------------------------------------------

// TestApproveOne_ConflictingSplitAddressFails is the split-address shape of
// the pre-existing "already carries a different tofu-address" refusal: a
// full, valid, ungapped continuation chain that just names a different
// address from the one this run wants is a rename, and Approve is never the
// one to perform a rename - that is choudoufu live-mv's job. Nothing is
// written.
func TestApproveOne_ConflictingSplitAddressFails(t *testing.T) {
	addr, _ := longAddr(t)
	otherAddr := mustAddr(t, longName+strings.Repeat("y", 300))
	otherChunks := discovery.SplitAddress(discovery.EscapeAddress(otherAddr.String()))
	if len(otherChunks) != 2 {
		t.Fatalf("test setup: the other address split into %d chunks, want 2", len(otherChunks))
	}

	e, p := vpcEligible(map[string]string{
		discovery.TagEstate:          "acme",
		discovery.TagAddress:         otherChunks[0],
		discovery.ContinuationTag(2): otherChunks[1],
	})

	out := approveOne(context.Background(), "acme", addr, e, "")

	if out.Outcome != OutcomeFailed {
		t.Fatalf("Outcome = %s, want FAILED (detail: %s)", out.Outcome, out.Detail)
	}
	if p.applyCount != 0 {
		t.Errorf("ApplyResourceChange was called %d times, want 0 for a conflicting marker", p.applyCount)
	}
	if !strings.Contains(out.Detail, "Already carries tofu-address") {
		t.Errorf("Detail = %q, want it to name the existing tofu-address conflict", out.Detail)
	}
}

// ---------------------------------------------------------------------------
// Corruption: an existing marker whose continuation chain has a gap
// ---------------------------------------------------------------------------

// TestApproveOne_CorruptContinuationChainFails: a resource that already
// claims this estate but whose continuation chain has a hole in it (a
// middle tag deleted by hand) cannot be compared to the wanted address at
// all, and approveOne refuses rather than guessing or overwriting - the
// same "malformed marker" refusal discovery's three scan paths give a
// gapped chain, extended to the write side.
func TestApproveOne_CorruptContinuationChainFails(t *testing.T) {
	addr := mustAddr(t, "aws_vpc.main")
	e, p := vpcEligible(map[string]string{
		discovery.TagEstate:          "acme",
		discovery.TagAddress:         strings.Repeat("a", 256),
		discovery.ContinuationTag(3): "b", // tofu-address-2 is missing.
	})

	out := approveOne(context.Background(), "acme", addr, e, "")

	if out.Outcome != OutcomeFailed {
		t.Fatalf("Outcome = %s, want FAILED (detail: %s)", out.Outcome, out.Detail)
	}
	if p.applyCount != 0 {
		t.Errorf("ApplyResourceChange was called %d times, want 0 for a corrupt continuation chain", p.applyCount)
	}
	if !strings.Contains(out.Detail, "continuation") || !strings.Contains(out.Detail, "gap") {
		t.Errorf("Detail = %q, want it to name the continuation gap", out.Detail)
	}
}

// ---------------------------------------------------------------------------
// BUG: the front-gap case markers.GatherAddress mishandles
// ---------------------------------------------------------------------------

// TestApproveOne_FrontGapBugSilentlyOverwritesStaleContinuation documents the
// downstream consequence of the n=2 gap pinned in
// internal/live/markers/markers_test.go's TestGatherAddress_frontGapIsCorrupt:
// tofu-address itself missing while a continuation tag - tofu-address-2 here
// - is present.
//
// Walk approveOne's switch through this case. The live object already
// carries this estate's tag (gotEstate == estate), and GatherAddress reports
// gotRaw == "" and corrupt == true:
//   - case gotEstate == estate && gotAddress == wantAddress && !corrupt:
//     false - gotAddress is "", not wantAddress.
//   - case gotEstate != "" && gotEstate != estate:
//     false - gotEstate does equal estate.
//   - case corrupt:
//     true - this is the one that fires.
//
// That is the same refusal every other shape of gapped chain gets (see
// TestApproveOne_CorruptContinuationChainFails above and discovery's
// TestContinuationGapIsMalformed): Outcome FAILED, nothing written, a human
// resolves the existing marker before this resource can be adopted. Before
// GatherAddress accounted for this gap, none of approveOne's four cases
// matched here, so it fell through to the write path as if this were a
// brand new adoption - desiredTags seeded from the existing tags (which
// still carried the stray tofu-address-2) and only overwrote the keys
// SplitAddress's chunks actually name, leaving the stray tofu-address-2
// behind. The write would have landed with tofu-address correctly naming
// addr, but a leftover tofu-address-2 sitting next to it - and the very next
// read of this resource, through GatherAddress, would have concatenated them
// into an address that names neither the old resource nor the new one.
func TestApproveOne_FrontGapBugSilentlyOverwritesStaleContinuation(t *testing.T) {
	addr := mustAddr(t, "aws_vpc.main") // fits in one chunk: only TagAddress is (re)written.
	e, p := vpcEligible(map[string]string{
		discovery.TagEstate:          "acme",  // already claims this estate ...
		discovery.ContinuationTag(2): "stale", // ... but tofu-address itself is missing.
	})

	out := approveOne(context.Background(), "acme", addr, e, "")

	if out.Outcome != OutcomeFailed {
		t.Errorf("Outcome = %s, want FAILED: a front-gapped continuation chain must be refused, not silently adopted over", out.Outcome)
	}
	if p.applyCount != 0 {
		t.Errorf("ApplyResourceChange was called %d times, want 0: nothing should be written over a corrupt marker", p.applyCount)
	}
	if _, present := p.appliedTags[discovery.ContinuationTag(2)]; present {
		t.Errorf("the stale tofu-address-2 = %q survived the write, producing a new corrupt marker on the very write meant to fix it", p.appliedTags[discovery.ContinuationTag(2)])
	}
}
