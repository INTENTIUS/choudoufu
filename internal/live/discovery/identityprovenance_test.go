// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// GitHub issue #620. [projection.builder.refuseListedButAbsent] (#596) turns
// an ABSENT provider answer into a refusal instead of a proposed create, and
// the only thing that keeps that refusal narrow is one property of the data
// it reads:
//
//	A non-null identity.Resolution.Identity means the PROVIDER'S OWN LIVE
//	ENUMERATION returned this object in this run. The estate's tag index
//	cannot produce one.
//
// Why the narrowness is the correctness property: the Resource Groups
// Tagging API keeps deleted objects queryable for a while, so "the tag index
// mentions it" is not evidence the object exists. A refusal keyed on that
// would block legitimate rebuilds of resources that really were destroyed,
// which is the opposite failure and no better. A refusal keyed on a live
// enumeration blocks only the case where the provider itself contradicts
// itself inside one run.
//
// Until this file the property was asserted in a doc comment and enforced
// nowhere. Nothing failed if a future discovery change hung a tag-index
// identity on a resolution, and the refusal would have widened silently.
//
// Three checks, deliberately of different kinds:
//
//   - [TestTagIndexBoundInstanceCarriesNoProviderIdentity] and
//     [TestTagSweptOrphanCarriesNoProviderIdentity] assert the BEHAVIOUR
//     through the whole pipeline, each against a control sighted by the
//     provider's list call that must carry a non-null identity. A refactor
//     cannot defeat them and neither can a rename.
//   - [TestIdentityProvenanceSitesAreEnumerated] is the structural backstop,
//     and it is a tripwire rather than a proof - see its own comment for
//     what it does and does not establish.

// TestTagIndexBoundInstanceCarriesNoProviderIdentity is the DECLARED half of
// the invariant, over the tag-index route a declared instance can actually
// take: [scanTypeMarkerFallback] (#293), which binds an instance of a type
// with no list route at all from the estate's tag index.
//
// The fixture is TestScanTypeMarkerFallbackBindsAnUnlistableTaggableType's:
// aws_route_table has no list route in this fake cloud, so the tag index is
// the only thing that finds aws_route_table.main. Every other instance is
// sighted by the provider's own ListResource, and the fake cloud serves an
// identity object for each - which is the control, and the reason this test
// can fail. Without it "identity is null" would pass on a fixture where
// nothing ever carries an identity at all.
func TestTagIndexBoundInstanceCarriesNoProviderIdentity(t *testing.T) {
	cloud := newFakeCloud()
	ownAllDiscovered(cloud)
	cloud.unlistable("aws_route_table")

	srv := &taggingServer{}
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:route-table/rtb-1", `aws_route_table.main`)

	res, diags := discoverFixture(t, cloud, taggingRequest(t, srv))
	assertNoErrors(t, diags)

	// The control first: if this half stops holding, the other half proves
	// nothing, so it is a Fatal rather than an Error.
	const control = `aws_vpc.main`
	cb, ok := res.BindingFor(mustAddr(t, control))
	if !ok {
		t.Fatalf("%s is not bound, so this test has no control:\n%s", control, res)
	}
	if !hasIdentityObject(cb.Identity) {
		t.Fatalf("%s was sighted by the provider's own list call and carries NO identity object (%#v).\n"+
			"That is the control for this test: with it gone, the assertion below passes vacuously.\n"+
			"Either the fake cloud stopped serving identities or the list path stopped carrying them.", control, cb.Identity)
	}
	if cr, ok := resolutionFor(res, control); !ok || !hasIdentityObject(cr.Identity) {
		t.Fatalf("%s's merged resolution carries no identity object, so the control does not reach the field the refusal reads:\n%s", control, res)
	}

	const subject = `aws_route_table.main`
	b, ok := res.BindingFor(mustAddr(t, subject))
	if !ok {
		t.Fatalf("%s is not bound, so nothing here was sighted through the tag index:\n%s", subject, res)
	}
	// Prove the sighting really was the tag index and not a list call that
	// happened to work: aws_route_table is unlistable in this fixture, so
	// its scan has to be the tagging source.
	scan, ok := res.ScanFor("aws_route_table")
	if !ok || scan.Source != SourceTagging {
		t.Fatalf("aws_route_table scan = %+v, want Source=%s - this test is not exercising the tag-index route", scan, SourceTagging)
	}
	if hasIdentityObject(b.Identity) {
		t.Errorf(tagIndexIdentityFailure, subject, "Binding.Identity", b.Identity)
	}
	r, ok := resolutionFor(res, subject)
	if !ok {
		t.Fatalf("%s has no merged resolution:\n%s", subject, res)
	}
	if hasIdentityObject(r.Identity) {
		t.Errorf(tagIndexIdentityFailure, subject, "identity.Resolution.Identity", r.Identity)
	}
}

// TestTagSweptOrphanCarriesNoProviderIdentity is the UNDECLARED half, over
// the other tag-index route: [sweepViaTagging] (#51), the estate-wide
// GetResources call that finds resources the configuration no longer
// declares. It files through the same [fileTaggingCandidate] the declared
// route uses, and reaches the projection as an OwnedResource rather than a
// Binding, so the field is a different one and worth its own assertion.
//
// Control and subject are the same live object under two sightings: the
// deleted aws_cloudwatch_log_group of sweep_test.go's
// TestSweepFindsDeletedBlock (provider list call, identity served) and of
// tagging_test.go's TestTaggingSweepFindsDeletedBlock (tag index only, no
// identity to serve).
func TestTagSweptOrphanCarriesNoProviderIdentity(t *testing.T) {
	const addr = `aws_cloudwatch_log_group.deleted`
	const importID = "/estate/deleted"

	t.Run("control/sighted by the provider's list call", func(t *testing.T) {
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		cloud.listable("aws_cloudwatch_log_group")
		cloud.own("aws_cloudwatch_log_group", importID, addr)

		res, diags := discoverFixture(t, cloud, Request{Sweep: true})
		assertNoErrors(t, diags)

		o, ok := removalsByAddr(res)[addr]
		if !ok {
			t.Fatalf("the deleted block's resource is not a removal:\n%s", res)
		}
		if !hasIdentityObject(o.Identity) {
			t.Fatalf("a natively swept orphan carries NO identity object (%#v), so this test has no control", o.Identity)
		}
		r, ok := resolutionFor(res, addr)
		if !ok || !hasIdentityObject(r.Identity) {
			t.Fatalf("the natively swept orphan's resolution carries no identity object, so the control does not reach the field the refusal reads:\n%s", res)
		}
	})

	t.Run("subject/sighted only by the estate's tag index", func(t *testing.T) {
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		// Deliberately not listable: if the type had a list route the sweep
		// would take it and this would be the control again.

		arn := "arn:aws:logs:us-east-1:123456789012:log-group:" + importID
		srv := &taggingServer{
			arns: []string{arn},
			tags: map[string]map[string]string{
				arn: {TagEstate: estateName, TagAddress: addr},
			},
		}
		server := srv.start(t)
		defer server.Close()

		res, diags := discoverFixture(t, cloud, Request{
			Sweep:        true,
			Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
			TaggingSweep: true,
			Roster:       taggingRoster(t, "aws_cloudwatch_log_group", "AWS::Logs::LogGroup", true),
		})
		assertNoErrors(t, diags)

		o, ok := removalsByAddr(res)[addr]
		if !ok {
			t.Fatalf("the deleted block's resource is not a removal, so nothing here came through the tag sweep:\n%s", res)
		}
		if !o.Swept {
			t.Fatalf("%s was not found by the sweep, so this is not the route under test", addr)
		}
		if hasIdentityObject(o.Identity) {
			t.Errorf(tagIndexIdentityFailure, addr, "OwnedResource.Identity", o.Identity)
		}
		r, ok := resolutionFor(res, addr)
		if !ok {
			t.Fatalf("%s has no merged resolution:\n%s", addr, res)
		}
		if hasIdentityObject(r.Identity) {
			t.Errorf(tagIndexIdentityFailure, addr, "identity.Resolution.Identity", r.Identity)
		}
	})
}

const tagIndexIdentityFailure = `%s was sighted ONLY by the estate's tag index, and its %s is non-null: %#v

GitHub issue #620. internal/live/projection's refuseListedButAbsent (#596)
reads exactly this field and treats a non-null value as proof the PROVIDER'S
OWN LIVE ENUMERATION returned this object in this run. The tag index is not
that: the Resource Groups Tagging API keeps deleted objects queryable, so a
tag-index sighting is not evidence the object exists.

With this value set, a resource that was genuinely destroyed but is still
lingering in the tag index stops being rebuilt and starts producing "Live
resource listed but not importable". That is a refusal fired at a user whose
configuration is correct.

Spell this field cty.NilVal on every path whose sighting is the tag index. If
the identity really was served by a provider list call on this path, say so in
refuseListedButAbsent's doc comment and in this file before relaxing anything
here.`

// hasIdentityObject is the same test [projection.builder.refuseListedButAbsent]
// makes on the value it receives (`w.identity == cty.NilVal ||
// w.identity.IsNull()`), so this file measures the field through the predicate
// that actually consumes it rather than through a spelling of its own.
func hasIdentityObject(v cty.Value) bool {
	return v != cty.NilVal && !v.IsNull()
}

func resolutionFor(res *Result, addr string) (identity.Resolution, bool) {
	for _, r := range res.Resolutions {
		if r.Addr.String() == addr {
			return r, true
		}
	}
	return identity.Resolution{}, false
}

// ---------------------------------------------------------------------------
// The structural backstop
// ---------------------------------------------------------------------------

// identityProvenanceSites is every place a production source in this package
// writes an identity object into a field that can reach
// [identity.Resolution.Identity], with the provenance each one carries.
//
// The two behaviour tests above cover the tag-index routes end to end. This
// list covers the rest of the surface, which is wider than issue #620's own
// inspection recorded: that inspection listed six claimant sites, and there
// are nineteen writes across four hops - claimant.identity, Binding.Identity,
// OwnedResource.Identity and identity.Resolution.Identity - plus
// UnclaimedResource, ParentReadFinding and ReconcileCandidate, which do not
// reach the refusal today but are the same field on the same objects.
var identityProvenanceSites = map[string]string{
	// claimant.identity - the declared instance's carrier.
	"cloudcontrol.go|claimant.identity|cty.NilVal":       "Cloud Control fallback: the identity is recomposed by hand from a CFN identifier, so no schema-matched object exists to carry.",
	"contentmatch.go|claimant.identity|cty.NilVal":       "content matching: same, and the match is on an argument value rather than on a marker.",
	"locatedfallback.go|claimant.identity|cty.NilVal":    "the record store: an identity read out of a local record, never off a live object.",
	"tagging.go|claimant.identity|cty.NilVal":            "THE TAG INDEX. See TestTagIndexBoundInstanceCarriesNoProviderIdentity and TestTagSweptOrphanCarriesNoProviderIdentity - fileTaggingCandidate serves both #51's estate sweep and #293's marker fallback.",
	"uniquename.go|claimant.identity|cty.NilVal":         "unique-name matching: a Cloud Control identifier, recomposed by hand.",
	"discovery.go|claimant.identity|claimIdentity":       "the provider's own ListResource result. claimIdentity is initialised from listclient.Result.Identity and is only ever reassigned to cty.NilVal (the identity-recomposing branch, where the served object's schema no longer matches the corrected type). THE ONLY non-null claimant provenance.",
	"displaced.go|listclient.Result.Identity|c.identity": "not a write into the pipeline: a claimant's own identity is wrapped back into a listclient.Result to reuse that type's IdentityAttr accessor.",

	// Binding.Identity - fed only from a claimant.
	"count.go|Binding.Identity|c.identity":     "a count/for_each slot member's claimant.",
	"discovery.go|Binding.Identity|c.identity": "bindClaimant, the ordinary declared binding.",

	// OwnedResource / UnclaimedResource - the sweep's own findings. The tag
	// sweep's orphan literal (tagging.go) sets no Identity field at all, which
	// is cty.NilVal, which is why there is no tagging.go row here.
	"discovery.go|OwnedResource.Identity|r.Identity":     "a natively swept orphan, off the provider's ListResource result.",
	"discovery.go|UnclaimedResource.Identity|r.Identity": "a foreign object, off the same result. Never bound to a declared address.",

	// identity.Resolution.Identity - the field the refusal reads.
	"discovery.go|identity.Resolution.Identity|b.Identity":   "a declared instance's Binding.",
	"discovery.go|identity.Resolution.Identity|s.Identity":   "a surplus count member's Binding (Result.Surplus is []Binding).",
	"discovery.go|identity.Resolution.Identity|o.Identity":   "an orphan's OwnedResource.",
	"fold_read.go|identity.Resolution.Identity|r.Identity":   "a folded parent read, off the provider's ListResource result. Undeclared, so refuseListedButAbsent returns early on it either way.",
	"parent_read.go|identity.Resolution.Identity|r.Identity": "a parent read, same.",

	// Findings that never become a resolution.
	"fold_read.go|ParentReadFinding.Identity|r.Identity":   "reporting only.",
	"parent_read.go|ParentReadFinding.Identity|r.Identity": "reporting only.",
	"reconcile.go|ReconcileCandidate.Identity|r.Identity":  "reporting only, off the provider's ListResource result.",
}

// TestIdentityProvenanceSitesAreEnumerated is a TRIPWIRE, not a proof, and
// the distinction matters enough to state plainly.
//
// What it establishes: no production source in this package gains, loses or
// respells a write into an identity field without a human recording where
// that identity came from. It parses the package's own sources, so a new
// file is covered the moment it is added.
//
// What it does NOT establish: that the expression on the right of a listed
// write really is a provider list result. It compares printed expressions, so
// it reads `r.Identity` as an identifier spelling and not as a
// listclient.Result. Renaming a variable trips it (which is the point - a
// human re-reads the site) but a NEW `r` of some other type spelled the same
// way would not be caught here. That gap is what the two behaviour tests
// above exist to close on the route that matters, and any new non-null
// provenance needs one of its own.
//
// A structural check is what is practical here: proving the type of each
// expression needs go/types over the whole package, and the property that
// actually protects users - "the value is null when the sighting was the tag
// index" - is directly observable end to end, which is the better instrument
// and is used above.
func TestIdentityProvenanceSitesAreEnumerated(t *testing.T) {
	found := map[string]token.Position{}
	fset := token.NewFileSet()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var parsed int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		parsed++
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			owner := exprString(fset, cl.Type)
			for _, el := range cl.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || (key.Name != "identity" && key.Name != "Identity") {
					continue
				}
				k := fmt.Sprintf("%s|%s.%s|%s", name, owner, key.Name, exprString(fset, kv.Value))
				found[k] = fset.Position(kv.Pos())
			}
			return true
		})
	}
	if parsed < 10 {
		t.Fatalf("parsed only %d production files in this package; the scan is not looking at the right directory", parsed)
	}

	var added, removed []string
	for k, pos := range found {
		if _, ok := identityProvenanceSites[k]; !ok {
			added = append(added, fmt.Sprintf("  %-28s %s", fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line), k))
		}
	}
	for k := range identityProvenanceSites {
		if _, ok := found[k]; !ok {
			removed = append(removed, "  "+k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	if len(added) > 0 {
		t.Errorf("a write into an identity field this package does not have a recorded provenance for:\n%s\n\n"+
			"GitHub issue #620. internal/live/projection's refuseListedButAbsent (#596) reads\n"+
			"identity.Resolution.Identity and treats a non-null value as proof the PROVIDER'S OWN LIVE\n"+
			"ENUMERATION returned the object in this run. Widening that is how choudoufu starts refusing\n"+
			"legitimate rebuilds of resources that really were destroyed.\n\n"+
			"What to do:\n"+
			"  1. If the sighting is the estate's tag index, a record, or anything recomposed by hand:\n"+
			"     spell the field cty.NilVal, and this test goes green with no edit here.\n"+
			"  2. If the identity really was served by a provider list call on this path: add a row to\n"+
			"     identityProvenanceSites saying so, extend refuseListedButAbsent's doc comment (it names\n"+
			"     the sites), and add a behaviour test on the model of\n"+
			"     TestTagIndexBoundInstanceCarriesNoProviderIdentity - with its control - for the new route.\n"+
			"  3. Never widen the refusal to make this easier. The narrowness IS the correctness property.",
			strings.Join(added, "\n"))
	}
	if len(removed) > 0 {
		t.Errorf("identityProvenanceSites lists writes this package no longer has:\n%s\n\n"+
			"If the site moved or was respelled, update the row. If it is genuinely gone, delete the row.",
			strings.Join(removed, "\n"))
	}
}

// TestTagIndexCarriersHoldNoIdentityObject is the tag-index half of the
// invariant stated as a property of the TYPES rather than of a code path, so
// that a future path cannot reach for an identity that is not there.
//
// [taggedCandidate] is what both tag-index routes file through
// ([sweepViaTagging]'s ARN join and [scanTypeMarkerFallback]'s marker walk),
// and [markerObject] is the index's own record of one tagged resource.
// Neither holds a cty.Value, so there is no provider identity object in scope
// at the point [fileTaggingCandidate] builds its claimant - the null it writes
// is the only value it could write.
func TestTagIndexCarriersHoldNoIdentityObject(t *testing.T) {
	ctyValue := reflect.TypeOf(cty.Value{})
	for _, ty := range []reflect.Type{
		reflect.TypeOf(taggedCandidate{}),
		reflect.TypeOf(markerObject{}),
	} {
		for i := 0; i < ty.NumField(); i++ {
			if f := ty.Field(i); f.Type == ctyValue {
				t.Errorf("%s.%s is a cty.Value.\n\n"+
					"GitHub issue #620. This type carries the estate's TAG INDEX into fileTaggingCandidate, and a\n"+
					"cty.Value on it is an identity object in scope where there must not be one: the next change\n"+
					"to that function can hang it on the claimant, and internal/live/projection's\n"+
					"refuseListedButAbsent (#596) would then read a tag-index sighting as proof the provider's own\n"+
					"live enumeration returned the object. The Tagging API keeps deleted objects queryable, so that\n"+
					"turns a legitimate rebuild into a refusal.\n\n"+
					"Carry whatever this is on a field that is not a cty.Value, or explain here why an identity\n"+
					"object on a tag-index carrier is safe.", ty.Name(), f.Name)
			}
		}
	}
}

func exprString(fset *token.FileSet, e ast.Expr) string {
	if e == nil {
		return "<inferred>"
	}
	var b strings.Builder
	if err := printer.Fprint(&b, fset, e); err != nil {
		return fmt.Sprintf("<unprintable %T>", e)
	}
	return b.String()
}
