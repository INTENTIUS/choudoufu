// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// ---------------------------------------------------------------------------
// scopePropertyIndex / identifierMatchesParent: the pure functions that
// decide whether a scoped listing's own result can be trusted, independent
// of any HTTP or identity-table machinery.
// ---------------------------------------------------------------------------

func TestScopePropertyIndex(t *testing.T) {
	tests := []struct {
		name              string
		primaryIdentifier []string
		scopeProperty     string
		wantIdx           int
		wantOK            bool
	}{
		{"first position (AWS::ApiGateway::Resource)", []string{"RestApiId", "ResourceId"}, "RestApiId", 0, true},
		{"second position (AWS::ApiGateway::Deployment)", []string{"DeploymentId", "RestApiId"}, "RestApiId", 1, true},
		{"absent entirely (AWS::DataZone::Project)", []string{"DomainId", "Id"}, "DomainIdentifier", 0, false},
		{"empty primary identifier", nil, "RestApiId", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, ok := scopePropertyIndex(tt.primaryIdentifier, tt.scopeProperty)
			if ok != tt.wantOK || (ok && idx != tt.wantIdx) {
				t.Errorf("scopePropertyIndex(%v, %q) = (%d, %v), want (%d, %v)", tt.primaryIdentifier, tt.scopeProperty, idx, ok, tt.wantIdx, tt.wantOK)
			}
		})
	}
}

func TestIdentifierMatchesParent(t *testing.T) {
	tests := []struct {
		name       string
		identifier string
		scopeIndex int
		arity      int
		parentVal  string
		want       bool
	}{
		{"matches at index 0", "rest-api-1|res-1", 0, 2, "rest-api-1", true},
		{"matches at index 1", "dep-1|rest-api-1", 1, 2, "rest-api-1", true},
		{"belongs to a different parent - the floci hazard", "rest-api-2|res-1", 0, 2, "rest-api-1", false},
		{"wrong arity (backend sent something else)", "rest-api-1", 0, 2, "rest-api-1", false},
		{"scope index out of range", "onlyone", 1, 2, "rest-api-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := identifierMatchesParent(tt.identifier, tt.scopeIndex, tt.arity, tt.parentVal)
			if got != tt.want {
				t.Errorf("identifierMatchesParent(%q, %d, %d, %q) = %v, want %v", tt.identifier, tt.scopeIndex, tt.arity, tt.parentVal, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parentScopedCloudControlSweepType: the full leg, driven directly (not
// through [Discover] - nothing in this package's [Discover] pipeline calls
// it yet, see this file's package doc in cloudcontrol_scoped.go for why).
// ---------------------------------------------------------------------------

// scopedRoster builds a *registry.Roster fixture carrying
// list_required_input and primary_identifier, which the package's existing
// ccRoster helper does not set - this leg's whole point is data neither of
// its sibling legs reads.
func scopedRoster(t *testing.T, tfType, cfnType string, primaryIdentifier, requiredInput []string, taggable bool) *registry.Roster {
	t.Helper()

	mappingJSON, err := json.Marshal(struct {
		Rows []struct {
			TFType  string  `json:"tf_type"`
			CFNType *string `json:"cfn_type"`
			Via     string  `json:"via"`
		} `json:"rows"`
	}{Rows: []struct {
		TFType  string  `json:"tf_type"`
		CFNType *string `json:"cfn_type"`
		Via     string  `json:"via"`
	}{{TFType: tfType, CFNType: &cfnType, Via: "name"}}})
	if err != nil {
		t.Fatalf("marshaling mapping fixture: %v", err)
	}

	registryJSON, err := json.Marshal(struct {
		Types []struct {
			TypeName          string   `json:"type_name"`
			PrimaryIdentifier []string `json:"primary_identifier,omitempty"`
			Tagging           struct {
				Taggable bool `json:"taggable"`
			} `json:"tagging"`
			Handlers struct {
				List              bool     `json:"list"`
				ListRequiredInput []string `json:"list_required_input,omitempty"`
			} `json:"handlers"`
		} `json:"types"`
	}{Types: []struct {
		TypeName          string   `json:"type_name"`
		PrimaryIdentifier []string `json:"primary_identifier,omitempty"`
		Tagging           struct {
			Taggable bool `json:"taggable"`
		} `json:"tagging"`
		Handlers struct {
			List              bool     `json:"list"`
			ListRequiredInput []string `json:"list_required_input,omitempty"`
		} `json:"handlers"`
	}{{
		TypeName:          cfnType,
		PrimaryIdentifier: primaryIdentifier,
		Tagging: struct {
			Taggable bool `json:"taggable"`
		}{Taggable: taggable},
		Handlers: struct {
			List              bool     `json:"list"`
			ListRequiredInput []string `json:"list_required_input,omitempty"`
		}{List: true, ListRequiredInput: requiredInput},
	}}})
	if err != nil {
		t.Fatalf("marshaling registry fixture: %v", err)
	}

	r, err := registry.Parse(mappingJSON, registryJSON)
	if err != nil {
		t.Fatalf("registry.Parse: %v", err)
	}
	return r
}

// scopedResult builds a *Result carrying one concrete resolution for a
// parent instance, the minimal state [parentScopedCloudControlSweepType]
// reads from res.Resolutions.
func scopedResult(t *testing.T, parentType, parentAddr, parentImportID string) *Result {
	t.Helper()
	return &Result{Verdicts: Verdicts{Resolutions: []identity.Resolution{
		{Addr: mustAddr(t, parentAddr), Class: identity.ClassConcrete, ImportID: parentImportID},
	}}}
}

// TestParentScopedCloudControlSweepTypeFindsChildrenAcrossMultipleParents is
// the headline: a parent can have zero, one, or many children - unlike
// [identity.SingleParentComponent]'s named-singleton shape - and this leg
// has to report every one it finds under the parent it resolved, not stop
// at the first. This test also uses arity-1 identifiers (no "|" at all)
// deliberately: composing a real multi-part identifier needs an
// internal/live/identity.DefaultTable entry this leg cannot add (forbidden
// surface, see the report), so the arity-2 composition step is exercised
// separately below, in
// TestParentScopedCloudControlSweepTypeDefendsAgainstAnUnscopedBackend, where
// its refusal (ProblemUncomposableIdentifier) - not a wrong answer - is part
// of what is under test alongside the scoping defense.
func TestParentScopedCloudControlSweepTypeFindsChildrenAcrossMultipleParents(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources["AWS::Test::ScopedChild"] = []ccResource{
		{identifier: "child-a"},
		{identifier: "child-b"},
	}
	server := srv.start()
	defer server.Close()

	req := Request{
		Estate:       estateName,
		Config:       ccConfig("aws_test_parent", "aws_test_scoped_child"),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL}),
		Roster: scopedRoster(t, "aws_test_scoped_child", "AWS::Test::ScopedChild",
			[]string{"ChildId"}, []string{"ChildId"}, false),
	}
	res := scopedResult(t, "aws_test_parent", "aws_test_parent.x", "child-a")
	// A single-property primary_identifier (arity 1) makes the scope
	// property itself the whole identifier, so both "children" returned
	// are - by this leg's own defense - only accepted if their identifier
	// equals the parent value. Exercise that against a scope that matches
	// one candidate to prove per-result filtering, not batch accept/reject.
	spec := ParentScopedChildSpec{TypeName: "aws_test_scoped_child", Parent: "aws_test_parent", CFNScopeProperty: "ChildId"}

	diags := parentScopedCloudControlSweepType(context.Background(), req, spec, res)
	assertNoErrors(t, diags)

	f, ok := findParentRead(res, "aws_test_scoped_child", "child-a")
	if !ok {
		t.Fatalf("no parent-scoped finding for child-a:\n%v", res.ParentReads)
	}
	if f.Parent != "aws_test_parent" || f.ParentValue != "child-a" {
		t.Errorf("finding = %+v, want Parent=aws_test_parent ParentValue=child-a", f)
	}
	if f.Withheld == "" {
		t.Error("finding should be Withheld (this leg never proposes removal on its own)")
	}
	if _, ok := findParentRead(res, "aws_test_scoped_child", "child-b"); ok {
		t.Error("child-b matched the scope property by coincidence of this fixture's own arity-1 shape, but should not have - only child-a equals the parent value")
	}
	for _, gap := range res.SweepGaps {
		t.Errorf("unexpected sweep gap: %s", gap)
	}
}

// TestParentScopedCloudControlSweepTypeDefendsAgainstAnUnscopedBackend is
// the floci hazard made concrete: the fake server (like floci's own
// ListResources - see internal/live/cloudcontrol.Client.ListResourcesScoped's
// doc comment, verified against floci's source) ignores ResourceModel
// entirely and returns every live child of the type, for every parent that
// asks. Two parents are resolved; the server always answers with children
// of both. Each parent must end up owning only its own child.
func TestParentScopedCloudControlSweepTypeDefendsAgainstAnUnscopedBackend(t *testing.T) {
	srv := newCCServer(t)
	// A composite (arity-2) identifier this time - RestApiId first, matching
	// AWS::ApiGateway::Resource's own shape - so the defense is exercised
	// against the real positional convention, not just an arity-1
	// coincidence.
	srv.listResources["AWS::Test::ScopedChild"] = []ccResource{
		{identifier: "api-1|res-1"},
		{identifier: "api-2|res-2"},
	}
	server := srv.start()
	defer server.Close()

	req := Request{
		Estate:       estateName,
		Config:       ccConfig("aws_test_parent", "aws_test_scoped_child"),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL}),
		Roster: scopedRoster(t, "aws_test_scoped_child", "AWS::Test::ScopedChild",
			[]string{"ParentId", "ChildId"}, []string{"ParentId"}, false),
	}
	res := &Result{Verdicts: Verdicts{Resolutions: []identity.Resolution{
		{Addr: mustAddr(t, "aws_test_parent.a"), Class: identity.ClassConcrete, ImportID: "api-1"},
		{Addr: mustAddr(t, "aws_test_parent.b"), Class: identity.ClassConcrete, ImportID: "api-2"},
	}}}
	spec := ParentScopedChildSpec{TypeName: "aws_test_scoped_child", Parent: "aws_test_parent", CFNScopeProperty: "ParentId"}

	// Diagnostics are not asserted error-free here: composing "ParentId|ChildId"
	// into a TF import ID needs an internal/live/identity.DefaultTable entry
	// this unmapped test type does not have, so every match this leg's own
	// scoping correctly lets through still ends in ProblemUncomposableIdentifier
	// - an honest, loud refusal, not a crash or a wrong answer. That refusal
	// is what the assertions below check for, precisely per parent.
	_ = parentScopedCloudControlSweepType(context.Background(), req, spec, res)

	// Both calls happened (one per parent instance - the cost model the
	// report names as forced by list_required_input being mandatory, not
	// optional). The composed import ID needs identity.DefaultTable
	// (Components for "ParentId|ChildId"), which this unmapped test type
	// does not have, so every match surfaces as a Problem rather than a
	// finding - which is fine here: what this test asserts is that the
	// server was called exactly twice (once per parent) and that neither
	// call's result leaked into a Problem naming the WRONG parent's raw
	// identifier, i.e. the filter ran before composition was even
	// attempted.
	if len(srv.calls) != 2 {
		t.Fatalf("expected 2 ListResources calls (one per parent instance), got %d: %v", len(srv.calls), srv.calls)
	}
	for _, p := range res.Problems {
		if p.Kind != ProblemUncomposableIdentifier {
			continue
		}
		for _, id := range p.LiveIDs {
			if id != "api-1|res-1" && id != "api-2|res-2" {
				t.Errorf("unexpected identifier reached composition: %q", id)
			}
		}
	}
	if len(res.Problems) != 2 {
		t.Fatalf("expected exactly 2 uncomposable-identifier problems (one per parent's own matching child), got %d: %v", len(res.Problems), res.Problems)
	}
}

// TestParentScopedCloudControlSweepTypeSkipsAnUnresolvedParent: a parent
// with no concrete resolution yet (not yet applied, or otherwise
// unresolved) is not iterated at all - zero Cloud Control calls, zero
// findings, zero gaps. This is the clean-skip half of the report's second
// design question: an apply-time-unknown parent is not a failure to sweep,
// it is nothing to sweep yet.
func TestParentScopedCloudControlSweepTypeSkipsAnUnresolvedParent(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources["AWS::Test::ScopedChild"] = []ccResource{{identifier: "child-a"}}
	server := srv.start()
	defer server.Close()

	req := Request{
		Estate:       estateName,
		Config:       ccConfig("aws_test_parent", "aws_test_scoped_child"),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL}),
		Roster: scopedRoster(t, "aws_test_scoped_child", "AWS::Test::ScopedChild",
			[]string{"ChildId"}, []string{"ChildId"}, false),
	}
	res := &Result{Verdicts: Verdicts{Resolutions: []identity.Resolution{
		// Needs discovery, not concrete: the parent itself has not been
		// resolved to a live value this pass.
		{Addr: mustAddr(t, "aws_test_parent.x"), Class: identity.ClassNeedsDiscovery},
	}}}
	spec := ParentScopedChildSpec{TypeName: "aws_test_scoped_child", Parent: "aws_test_parent", CFNScopeProperty: "ChildId"}

	diags := parentScopedCloudControlSweepType(context.Background(), req, spec, res)
	assertNoErrors(t, diags)

	if len(srv.calls) != 0 {
		t.Errorf("expected zero Cloud Control calls for an unresolved parent, got %v", srv.calls)
	}
	if len(res.ParentReads) != 0 {
		t.Errorf("expected zero findings for an unresolved parent, got %v", res.ParentReads)
	}
}

// TestParentScopedCloudControlSweepTypeRefusesRequiredInputMismatch is the
// loud-not-silent guarantee for a spec whose CFNScopeProperty does not match
// what the registry actually requires: [SweepGapScopeUnavailable], not an
// empty result masquerading as "nothing found".
func TestParentScopedCloudControlSweepTypeRefusesRequiredInputMismatch(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources["AWS::Test::ScopedChild"] = []ccResource{{identifier: "child-a"}}
	server := srv.start()
	defer server.Close()

	req := Request{
		Estate:       estateName,
		Config:       ccConfig("aws_test_parent", "aws_test_scoped_child"),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL}),
		// The registry requires "OtherId", not "ChildId".
		Roster: scopedRoster(t, "aws_test_scoped_child", "AWS::Test::ScopedChild",
			[]string{"OtherId"}, []string{"OtherId"}, false),
	}
	res := scopedResult(t, "aws_test_parent", "aws_test_parent.x", "parent-1")
	spec := ParentScopedChildSpec{TypeName: "aws_test_scoped_child", Parent: "aws_test_parent", CFNScopeProperty: "ChildId"}

	diags := parentScopedCloudControlSweepType(context.Background(), req, spec, res)
	assertNoErrors(t, diags)

	if len(srv.calls) != 0 {
		t.Errorf("expected zero Cloud Control calls when the required input does not match, got %v", srv.calls)
	}
	gap, ok := findSweepGap(res, "aws_test_scoped_child")
	if !ok {
		t.Fatal("expected a SweepGap for the required-input mismatch, found none")
	}
	if gap.Reason != SweepGapScopeUnavailable {
		t.Errorf("gap reason = %q, want %q", gap.Reason, SweepGapScopeUnavailable)
	}
}

// TestParentScopedCloudControlSweepTypeRefusesUnverifiableScope is the same
// loudness guarantee for the DataZone shape the report names precisely:
// list_required_input names a property ("DomainIdentifier") that does not
// appear in the type's own primary_identifier at all, so a scoped result's
// identifier cannot be checked against the parent value it was scoped to -
// this leg must refuse rather than trust the backend blindly (floci does
// not honor ResourceModel; even against real AWS, positional verification
// is this leg's only defense in depth).
func TestParentScopedCloudControlSweepTypeRefusesUnverifiableScope(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources["AWS::Test::ScopedChild"] = []ccResource{{identifier: "id-a"}}
	server := srv.start()
	defer server.Close()

	req := Request{
		Estate:       estateName,
		Config:       ccConfig("aws_test_parent", "aws_test_scoped_child"),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL}),
		// DomainId/Id in the identifier; DomainIdentifier is required to
		// scope but is not either of those - the DataZone shape.
		Roster: scopedRoster(t, "aws_test_scoped_child", "AWS::Test::ScopedChild",
			[]string{"DomainId", "Id"}, []string{"DomainIdentifier"}, false),
	}
	res := scopedResult(t, "aws_test_parent", "aws_test_parent.x", "domain-1")
	spec := ParentScopedChildSpec{TypeName: "aws_test_scoped_child", Parent: "aws_test_parent", CFNScopeProperty: "DomainIdentifier"}

	diags := parentScopedCloudControlSweepType(context.Background(), req, spec, res)
	assertNoErrors(t, diags)

	if len(srv.calls) != 0 {
		t.Errorf("expected zero Cloud Control calls when the scope cannot be positionally verified, got %v", srv.calls)
	}
	gap, ok := findSweepGap(res, "aws_test_scoped_child")
	if !ok {
		t.Fatal("expected a SweepGap for the unverifiable scope, found none")
	}
	if gap.Reason != SweepGapScopeUnavailable {
		t.Errorf("gap reason = %q, want %q", gap.Reason, SweepGapScopeUnavailable)
	}
}

// TestParentScopedCloudControlSweepTypeReportsListFailureLoudly proves the
// call-failure path is a warning-carrying [SweepGapListFailed], not a
// swallowed error - the sweepViaTagging shape (#229) this leg must not
// repeat.
func TestParentScopedCloudControlSweepTypeReportsListFailureLoudly(t *testing.T) {
	srv := newCCServer(t)
	srv.listErr["AWS::Test::ScopedChild"] = cloudcontrol.CodeValidationError
	server := srv.start()
	defer server.Close()

	req := Request{
		Estate:       estateName,
		Config:       ccConfig("aws_test_parent", "aws_test_scoped_child"),
		CloudControl: cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1}),
		Roster: scopedRoster(t, "aws_test_scoped_child", "AWS::Test::ScopedChild",
			[]string{"ChildId"}, []string{"ChildId"}, false),
	}
	res := scopedResult(t, "aws_test_parent", "aws_test_parent.x", "parent-1")
	spec := ParentScopedChildSpec{TypeName: "aws_test_scoped_child", Parent: "aws_test_parent", CFNScopeProperty: "ChildId"}

	diags := parentScopedCloudControlSweepType(context.Background(), req, spec, res)
	if diags.HasErrors() {
		t.Fatalf("expected a warning, not an error, for the failed list call: %s", diags.Err())
	}
	if len(diags) == 0 {
		t.Fatal("expected a warning diagnostic for the failed list call, got none")
	}
	gap, ok := findSweepGap(res, "aws_test_scoped_child")
	if !ok {
		t.Fatal("expected a SweepGap for the failed list call, found none")
	}
	if gap.Reason != SweepGapListFailed {
		t.Errorf("gap reason = %q, want %q", gap.Reason, SweepGapListFailed)
	}
	for _, tn := range res.SweepCovered {
		if tn == "aws_test_scoped_child" {
			t.Error("a type whose list call failed must not be recorded as SweepCovered")
		}
	}
}

func findSweepGap(res *Result, typeName string) (SweepGap, bool) {
	for _, g := range res.SweepGaps {
		if g.TypeName == typeName {
			return g, true
		}
	}
	return SweepGap{}, false
}
