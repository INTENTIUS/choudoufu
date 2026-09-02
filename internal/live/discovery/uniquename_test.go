// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// These tests cover the name-binding leg (GitHub issue #272,
// internal/live/discovery/uniquename.go): recognising a listed object by the
// name the configuration gave it, where AWS itself guarantees that name
// identifies at most one object per account and region.
//
// They name a resource type, which the standing bar allows a test to do and
// forbids a generator. aws_cloudfront_cache_policy is used because it is a
// REAL admitted row - its UniqueName comes out of the committed table, which
// comes out of two committed artifacts - so a change that emptied the
// evidence, or wrote the wrong property path into the row, fails these tests
// rather than passing against a fixture that agreed with itself.

const cachePolicyTF = "aws_cloudfront_cache_policy"
const cachePolicyCFN = "AWS::CloudFront::CachePolicy"

// uniqueNameProps builds a Cloud Control Properties map carrying a cache
// policy's name at the nested path the CloudFormation schema puts it,
// reading the path off the committed table rather than spelling
// "CachePolicyConfig" here. A test that hard-coded the path would keep
// passing if the row's path were wrong, which is the one thing these tests
// exist to catch.
func uniqueNameProps(t *testing.T, name string) map[string]any {
	t.Helper()
	ti, ok := identity.LookupType(cachePolicyTF)
	if !ok || !ti.UniqueName.Set() {
		t.Fatalf("%s carries no UniqueName in the committed identity table, so this whole file tests nothing", cachePolicyTF)
	}
	path := ti.UniqueName.PropertyPath()
	var cur any = name
	for i := len(path) - 1; i >= 0; i-- {
		cur = map[string]any{path[i]: cur}
	}
	m, _ := cur.(map[string]any)
	return m
}

// uniqueNameRequest wires one config, one fake Cloud Control endpoint and one
// resolution per declared instance. names maps a block name to the name its
// configuration states; an empty value means the instance's name did not
// resolve, which is what an unevaluable name argument produces.
func uniqueNameRequest(t *testing.T, srv *ccServer, names map[string]string) Request {
	t.Helper()

	resources := make(map[string]*configs.Resource, len(names))
	var resolutions []identity.Resolution
	for blockName, name := range names {
		addr := cachePolicyTF + "." + blockName
		resources[addr] = &configs.Resource{
			Mode:      addrs.ManagedResourceMode,
			Type:      cachePolicyTF,
			Name:      blockName,
			DeclRange: hcl.Range{Filename: "uniquename_test.go"},
		}
		r := identity.Resolution{
			Addr:  mustAddr(t, addr),
			Class: identity.ClassNeedsDiscovery,
			Cause: identity.DiscoveryServerAssigned,
		}
		if name != "" {
			r.Cause = identity.DiscoveryUniqueName
			r.CauseArgs = []string{"name"}
			r.UniqueName = name
		}
		resolutions = append(resolutions, r)
	}

	server := srv.start()
	t.Cleanup(server.Close)

	return Request{
		Estate:       estateName,
		Config:       &configs.Config{Module: &configs.Module{ManagedResources: resources}},
		Resolutions:  resolutions,
		Provider:     newFakeCloud(), // knows nothing of this type, so Cloud Control is the source
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL}),
		Roster: ccRoster(t,
			map[string]string{cachePolicyTF: cachePolicyCFN},
			map[string]bool{cachePolicyCFN: true},
			map[string]bool{cachePolicyCFN: false}, // untaggable, which is why it needs this leg
		),
	}
}

func uniqueNameProblems(res *Result, kind ProblemKind) []Problem {
	var out []Problem
	for _, p := range res.Problems {
		if p.Kind == kind {
			out = append(out, p)
		}
	}
	return out
}

// TestUniqueNameBindsTheSoleMatch is the positive case: one declared name,
// one live object carrying it, bound - with no ownership marker anywhere in
// the exchange, which is the whole point.
func TestUniqueNameBindsTheSoleMatch(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources[cachePolicyCFN] = []ccResource{
		{identifier: "658327ea-f89d-4fab-a63d-7e88639e58f6", properties: uniqueNameProps(t, "static-assets")},
		{identifier: "ccca32ef-dce3-4df3-80df-1bd3000bc4d3", properties: uniqueNameProps(t, "somebody-elses-policy")},
	}

	res, diags := Discover(context.Background(), uniqueNameRequest(t, srv, map[string]string{"app": "static-assets"}))
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}

	b, ok := res.BindingFor(mustAddr(t, cachePolicyTF+".app"))
	if !ok {
		t.Fatalf("the sole name match did not bind:\n%s", res)
	}
	if b.ImportID != "658327ea-f89d-4fab-a63d-7e88639e58f6" {
		t.Errorf("bound to import ID %q, want the identifier of the object whose name matched", b.ImportID)
	}
	scan, _ := res.ScanFor(cachePolicyTF)
	if scan.NameBound != 1 {
		t.Errorf("scan reports NameBound = %d, want 1", scan.NameBound)
	}
	// The second listed object is somebody else's. It must be left entirely
	// alone: not bound, not unclaimed, not an orphan. There is no ownership
	// marker on this type anywhere, so nothing about an unmatched object says
	// this estate created it, and calling one an orphan puts it up for
	// destruction.
	if len(res.Orphans) != 0 {
		t.Errorf("a listed object matching no declared name became an orphan (%d), which would put somebody else's resource up for destruction: %v", len(res.Orphans), res.Orphans)
	}
	if len(res.Unclaimed) != 0 {
		t.Errorf("a listed object matching no declared name was reported unclaimed (%d): %v", len(res.Unclaimed), res.Unclaimed)
	}
}

// TestUniqueNameRefusesSeveralLiveMatches is the load-bearing guard, and the
// one worth breaking on purpose to confirm it fires.
//
// AWS's own documentation says two cache policies cannot share a name. If a
// listing shows two anyway, the guarantee this fork read off a document does
// not hold the way it was read - and picking either object would be exactly
// the content-match guess internal/live/foreign refuses to make. Nothing may
// bind.
func TestUniqueNameRefusesSeveralLiveMatches(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources[cachePolicyCFN] = []ccResource{
		{identifier: "policy-a", properties: uniqueNameProps(t, "static-assets")},
		{identifier: "policy-b", properties: uniqueNameProps(t, "static-assets")},
	}

	res, diags := Discover(context.Background(), uniqueNameRequest(t, srv, map[string]string{"app": "static-assets"}))

	if _, ok := res.BindingFor(mustAddr(t, cachePolicyTF+".app")); ok {
		t.Error("two live resources carried the declared name and one of them was bound anyway; that is the guess the marker spec exists to forbid")
	}
	problems := uniqueNameProblems(res, ProblemAmbiguousUniqueName)
	if len(problems) != 1 {
		t.Fatalf("got %d AMBIGUOUS_UNIQUE_NAME problem(s), want 1:\n%s", len(problems), res)
	}
	if got := problems[0].LiveIDs; len(got) != 2 {
		t.Errorf("the problem names %d live resources, want both of them: %v", len(got), got)
	}
	if !diags.HasErrors() {
		t.Error("the ambiguity was recorded as a problem but produced no error diagnostic; a refusal a run does not see is not a refusal")
	}
	scan, _ := res.ScanFor(cachePolicyTF)
	if scan.NameBound != 0 {
		t.Errorf("scan reports NameBound = %d after refusing to bind, want 0", scan.NameBound)
	}
}

// TestUniqueNameRefusesSeveralDeclaredInstances is the same guard from the
// other side. Two blocks stating one name is a configuration that cannot
// work, but this pass is not the one that says so - and binding one live
// object to two addresses would be a far worse answer than refusing.
func TestUniqueNameRefusesSeveralDeclaredInstances(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources[cachePolicyCFN] = []ccResource{
		{identifier: "policy-a", properties: uniqueNameProps(t, "static-assets")},
	}

	res, _ := Discover(context.Background(), uniqueNameRequest(t, srv, map[string]string{
		"app":   "static-assets",
		"other": "static-assets",
	}))

	for _, block := range []string{"app", "other"} {
		if _, ok := res.BindingFor(mustAddr(t, cachePolicyTF+"."+block)); ok {
			t.Errorf("%s bound while two declared instances claimed one unique name", block)
		}
	}
	problems := uniqueNameProblems(res, ProblemAmbiguousUniqueName)
	if len(problems) != 1 {
		t.Fatalf("got %d AMBIGUOUS_UNIQUE_NAME problem(s), want 1:\n%s", len(problems), res)
	}
	for _, block := range []string{"app", "other"} {
		if !strings.Contains(problems[0].Detail, cachePolicyTF+"."+block) {
			t.Errorf("the problem does not name %s, so an operator cannot tell which blocks collided:\n  %s", block, problems[0].Detail)
		}
	}
}

// TestUniqueNameDiagnosesAnUnreadableName is the zero case, and it is a
// diagnosis rather than a skip for a specific reason: the object may BE the
// one the declared instance is waiting for. Passing over it silently leaves
// the instance unbound, and an unbound instance is proposed for creation - so
// a listing this leg could not read would become a second live object beside
// the first.
func TestUniqueNameDiagnosesAnUnreadableName(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources[cachePolicyCFN] = []ccResource{
		// Present, listed, and carrying nothing at the property path: the
		// shape a schema change or a partial Cloud Control response produces.
		{identifier: "policy-a", properties: map[string]any{"SomethingElse": "x"}},
	}

	res, diags := Discover(context.Background(), uniqueNameRequest(t, srv, map[string]string{"app": "static-assets"}))

	problems := uniqueNameProblems(res, ProblemUnreadableUniqueName)
	if len(problems) != 1 {
		t.Fatalf("got %d UNREADABLE_UNIQUE_NAME problem(s), want 1:\n%s", len(problems), res)
	}
	if !diags.HasErrors() {
		t.Error("an unreadable listing produced no error diagnostic")
	}
	if _, ok := res.BindingFor(mustAddr(t, cachePolicyTF+".app")); ok {
		t.Error("an object whose name could not be read was bound anyway")
	}
}

// TestUniqueNameIgnoresAnInstanceWithNoResolvedName covers the per-instance
// half of the fail-closed rule. An instance whose name argument this run
// could not evaluate carries no [identity.Resolution.UniqueName], is not in
// the index, and binds to nothing - even with a live object sitting there
// that a looser rule would have matched.
func TestUniqueNameIgnoresAnInstanceWithNoResolvedName(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources[cachePolicyCFN] = []ccResource{
		{identifier: "policy-a", properties: uniqueNameProps(t, "static-assets")},
	}

	// The empty string means "this instance's name did not resolve".
	res, _ := Discover(context.Background(), uniqueNameRequest(t, srv, map[string]string{"app": ""}))

	if _, ok := res.BindingFor(mustAddr(t, cachePolicyTF+".app")); ok {
		t.Error("an instance whose name this run cannot compute was bound to a live object anyway")
	}
	// And it must not be blamed for the listing either: nothing about that
	// object was compared, so there is nothing to report about it.
	if got := uniqueNameProblems(res, ProblemAmbiguousUniqueName); len(got) != 0 {
		t.Errorf("got %d AMBIGUOUS_UNIQUE_NAME problem(s) for an instance nothing was compared against", len(got))
	}
}

// TestUniqueNameNeverReachesTheNoTagsRefusal pins the routing decision. A
// name-bound type has no tags argument at all, so sending it down the marker
// path would raise NO_TAGS for every listed object - a diagnostic blaming the
// provider for the absence of something this fork already knew was absent.
func TestUniqueNameNeverReachesTheNoTagsRefusal(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources[cachePolicyCFN] = []ccResource{
		{identifier: "policy-a", properties: uniqueNameProps(t, "static-assets")},
		{identifier: "policy-b", properties: uniqueNameProps(t, "somebody-elses")},
	}

	res, _ := Discover(context.Background(), uniqueNameRequest(t, srv, map[string]string{"app": "static-assets"}))

	if got := uniqueNameProblems(res, ProblemNoTags); len(got) != 0 {
		t.Errorf("got %d NO_TAGS problem(s) on a type that is recognised by its name and has no tags argument to lack", len(got))
	}
}

// TestCCPropertyStringIsStrict covers the reader itself. Every refusal below
// is a shape a looser reader would have turned into a value, and a value
// invented here is a live object bound to the wrong address.
func TestCCPropertyStringIsStrict(t *testing.T) {
	nested := map[string]any{"CachePolicyConfig": map[string]any{"Name": "static-assets"}}

	for _, tc := range []struct {
		name  string
		props map[string]any
		path  []string
		want  string
		ok    bool
	}{
		{"nested hit", nested, []string{"CachePolicyConfig", "Name"}, "static-assets", true},
		{"top-level hit", map[string]any{"Name": "x"}, []string{"Name"}, "x", true},
		{"absent leaf", nested, []string{"CachePolicyConfig", "Missing"}, "", false},
		{"absent parent", nested, []string{"Missing", "Name"}, "", false},
		{"path stops short of the leaf", nested, []string{"CachePolicyConfig"}, "", false},
		{"leaf is a number", map[string]any{"Name": 42.0}, []string{"Name"}, "", false},
		{"leaf is a list", map[string]any{"Name": []any{"x"}}, []string{"Name"}, "", false},
		{"descent through a non-object", map[string]any{"CachePolicyConfig": "x"}, []string{"CachePolicyConfig", "Name"}, "", false},
		{"empty path reads nothing", nested, nil, "", false},
		{"nil properties", nil, []string{"Name"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ccPropertyString(tc.props, tc.path)
			if got != tc.want || ok != tc.ok {
				t.Errorf("ccPropertyString = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestUniqueNameIndexSkipsInstancesWithNoName asserts the index's own
// membership rule directly, and it exists because asserting it through
// Discover does not work.
//
// Deleting the "no resolved name" skip in uniqueNameIndexFor leaves the whole
// end-to-end suite green: such an instance lands under the key "", and
// observe already refuses to record a live object whose own name is empty, so
// nothing ever matches it and the outcome is the same. The skip is the second
// of two guards against the same hazard - a declared instance with no name
// binding to a listed object with no name, which would be a match on the
// absence of information - and a guard whose only proof is that another guard
// also holds is a guard nobody will notice removing. This is its own proof.
func TestUniqueNameIndexSkipsInstancesWithNoName(t *testing.T) {
	named := &declaredEntry{escaped: "named", res: identity.Resolution{
		Addr: mustAddr(t, cachePolicyTF+".named"), Class: identity.ClassNeedsDiscovery, UniqueName: "static-assets",
	}}
	unnamed := &declaredEntry{escaped: "unnamed", res: identity.Resolution{
		Addr: mustAddr(t, cachePolicyTF+".unnamed"), Class: identity.ClassNeedsDiscovery,
	}}
	d := &declared{types: map[string]map[string]*declaredEntry{
		cachePolicyTF: {"named": named, "unnamed": unnamed},
	}}

	idx, ok := uniqueNameIndexFor(d, cachePolicyTF)
	if !ok {
		t.Fatalf("%s built no index", cachePolicyTF)
	}
	if len(idx.declared) != 1 {
		t.Errorf("the index holds %d declared name(s), want 1 - the instance whose name did not resolve is in it", len(idx.declared))
	}
	if _, present := idx.declared[""]; present {
		t.Error("an instance with no resolved name was indexed under the empty name; a live object whose own name came back empty would then match on the absence of information")
	}
	if got := idx.declared["static-assets"]; len(got) != 1 || got[0] != named {
		t.Errorf("the named instance is not indexed under its own name: %v", got)
	}
}

// TestUniqueNameIndexOnlyForNameBoundTypes is the negative half of the
// routing decision: a type whose row carries no UniqueName never builds an
// index, so nothing about this leg can touch the marker path.
func TestUniqueNameIndexOnlyForNameBoundTypes(t *testing.T) {
	d := &declared{types: map[string]map[string]*declaredEntry{}}
	for _, typeName := range []string{"aws_vpc", "aws_cloudfront_origin_access_control", "aws_not_a_real_type"} {
		if _, ok := uniqueNameIndexFor(d, typeName); ok {
			t.Errorf("%s built a unique-name index; only a row carrying UniqueName may", typeName)
		}
	}
	if _, ok := uniqueNameIndexFor(d, cachePolicyTF); !ok {
		t.Errorf("%s built no unique-name index, so every test in this file is exercising the marker path instead", cachePolicyTF)
	}
}
